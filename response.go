package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/leekchan/accounting"
	"github.com/line/line-bot-sdk-go/linebot"
	"log"
)

type textResponse interface {
	Do(args ...string) (string, error)
}

type push struct {
	UserID     string     `json:"user_id"`
	Currency   string     `json:"currency"`
	Interval   int        `json:"interval"`
	CheckAlert float64    `json:"check_alert"`
	CheckRange float64    `json:"check_range"`
	PushedAt   *time.Time `json:"pushed_at"`
}

type generalResponse struct{}

func (g generalResponse) Do(args ...string) (string, error) {
	return "ตอบไม่ได้อ่ะ เสียใจ T^T", nil
}

type helpResponse struct{}

func (h helpResponse) Do(args ...string) (string, error) {
	rtn := "ทำแบบนี้ๆ"
	rtn += "\n1. curr ${currency}"
	rtn += "\n2. setinterval ${currency} ${minutes}"
	rtn += "\n3. viewinterval"
	rtn += "\n4. removeinterval ${currency}"
	rtn += "\n5. setalert ${currency} ${range}"
	rtn += "\n6. viewalert"
	rtn += "\n7. removealert ${currency}"

	return rtn, nil
}

type currentResponse struct{}

func (c currentResponse) Do(args ...string) (string, error) {
	curs, err := getBXCurrency()
	if err != nil {
		return "", errors.New("error เบย " + err.Error())
	}
	curr := curs.GetByName(args[1])

	return fmt.Sprintf("ค่าเงิน %s: %s", args[1], accounting.FormatNumberFloat64(curr.LastPrice, 2, ",", ".")), nil
}

type setIntervalResponse struct {
	Source *linebot.EventSource
}

func (s setIntervalResponse) Do(args ...string) (string, error) {
	curr := strings.ToLower(args[1])
	ti, err := strconv.Atoi(args[2])
	if err != nil {
		return "", errors.New("ส่งเวลาเป็นตัวเลขด้วยจ้าาาา")
	}
	if ti != 10 {
		return "", errors.New("ตอนนี้ได้แค่ทุกๆ 10 นาทีก่อนนะ อิอิ")
	}

	hkey := fmt.Sprintf("%s:%s:interval", s.Source.UserID, curr)
	skey := fmt.Sprintf("interval:%s", curr)

	tn := time.Now()
	p := push{
		UserID:   s.Source.UserID,
		Currency: curr,
		Interval: ti,
		PushedAt: &tn,
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", errors.New("สร้าง json บ่ได้")
	}

	conn := rdPool.Get()
	defer conn.Close()

	conn.Send("MULTI")
	conn.Send("SET", hkey, data)
	conn.Send("SADD", skey, hkey)
	if _, err := conn.Do("EXEC"); err != nil {
		return "", errors.New("redis พังอ่ะ " + err.Error())
	}
	return "ตั้งค่าเรียบร้อยคร๊าบบบบบ DED", nil
}

type removeIntervalResponse struct {
	Source *linebot.EventSource
}

func (r removeIntervalResponse) Do(args ...string) (string, error) {
	curr := strings.ToLower(args[1])
	conn := rdPool.Get()
	defer conn.Close()

	hkey := fmt.Sprintf("%s:%s:interval", r.Source.UserID, curr)
	skey := fmt.Sprintf("interval:%s", curr)

	log.Println("remove interval:", hkey, skey)

	conn.Send("MULTI")
	conn.Send("DEL", hkey)
	conn.Send("SREM", skey, hkey)
	if _, err := conn.Do("EXEC"); err != nil {
		return "", errors.New("redis พังอ่ะ " + err.Error())
	}
	return "ลบค่าเรียบร้อยยยยย", nil
}

type viewIntervalResponse struct {
	Source *linebot.EventSource
}

func (v viewIntervalResponse) Do(args ...string) (string, error) {
	conn := rdPool.Get()
	defer conn.Close()

	iter := 0
	key := fmt.Sprintf("%s:*:interval", v.Source.UserID)
	var keys []string
	for {
		if arr, err := redis.Values(conn.Do("SCAN", iter, "MATCH", key)); err == nil {
			iter, _ = redis.Int(arr[0], nil)
			vs, _ := redis.Strings(arr[1], nil)

			if len(vs) > 0 {
				keys = append(keys, vs...)
			}
		}
		if iter == 0 {
			break
		}
	}

	rtn := "ท่านตั้งค่า interval ดังนี้..."
	for _, k := range keys {
		data, err := redis.Bytes(conn.Do("GET", k))
		if err != nil {
			rtn += fmt.Sprintf("\nค่า: %s ดึงไม่ได้อ่ะ = =", k)
			continue
		}

		var p push
		if err := json.Unmarshal(data, &p); err != nil {
			rtn += fmt.Sprintf("\nค่า: %s ดึงไม่ได้อ่ะ = =", k)
			continue
		}
		rtn += fmt.Sprintf("\nค่าเงิน %s ยิงทุกๆ %d นาที ล่าสุด: %s", p.Currency, p.Interval, p.PushedAt.In(tz))
	}
	return rtn, nil
}

type flushAllResponse struct {
	Source *linebot.EventSource
}

func (f flushAllResponse) Do(args ...string) (string, error) {
	if f.Source.UserID != os.Getenv("ADMIN_TOKEN") {
		return "", errors.New("ไม่ให้ทำหรอก ชิชิ")
	}

	conn := rdPool.Get()
	defer conn.Close()

	if _, err := conn.Do("FLUSHALL"); err != nil {
		return "", errors.New("ลบไม่ได้จ้าาาา = =")
	}
	return "ลบข้อมูลแล้วนะ ลาก่อยยยยย", nil
}

type setAlertResponse struct {
	Source *linebot.EventSource
}

func (s setAlertResponse) Do(args ...string) (string, error) {
	curr := strings.ToLower(args[1])
	am, err := strconv.ParseFloat(args[2], 64)
	if err != nil {
		return "", errors.New("ส่งเงินเป็นตัวเลขเน้อออออ")
	}
	ra, err := strconv.ParseFloat(args[3], 64)
	if err != nil {
		return "", errors.New("ส่ง range เป็น float นะ")
	}

	hkey := fmt.Sprintf("%s:%s:alert", s.Source.UserID, curr)
	skey := fmt.Sprintf("alert:%s", curr)

	tn := time.Now()
	p := push{
		UserID:     s.Source.UserID,
		Currency:   curr,
		CheckAlert: am,
		CheckRange: ra,
		PushedAt:   &tn,
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", errors.New("สร้าง json บ่ได้")
	}

	conn := rdPool.Get()
	defer conn.Close()

	conn.Send("MULTI")
	conn.Send("SET", hkey, data)
	conn.Send("SADD", skey, hkey)
	if _, err := conn.Do("EXEC"); err != nil {
		return "", errors.New("redis พังอ่ะ " + err.Error())
	}
	return "ตั้งค่าเรียบร้อยคร๊าบบบบบ DED", nil
}

type removeAlertResponse struct {
	Source *linebot.EventSource
}

func (r removeAlertResponse) Do(args ...string) (string, error) {
	curr := strings.ToLower(args[1])
	conn := rdPool.Get()
	defer conn.Close()

	hkey := fmt.Sprintf("%s:%s:alert", r.Source.UserID, curr)
	skey := fmt.Sprintf("alert:%s", curr)

	conn.Send("MULTI")
	conn.Send("DEL", hkey)
	conn.Send("SREM", skey, hkey)
	if _, err := conn.Do("EXEC"); err != nil {
		return "", errors.New("redis พังอ่ะ " + err.Error())
	}
	return "ลบค่าเรียบร้อยยยยย", nil
}

type viewAlertResponse struct {
	Source *linebot.EventSource
}

func (v viewAlertResponse) Do(args ...string) (string, error) {
	conn := rdPool.Get()
	defer conn.Close()

	iter := 0
	key := fmt.Sprintf("%s:*:alert", v.Source.UserID)
	var keys []string
	for {
		if arr, err := redis.Values(conn.Do("SCAN", iter, "MATCH", key)); err == nil {
			iter, _ = redis.Int(arr[0], nil)
			vs, _ := redis.Strings(arr[1], nil)

			if len(vs) > 0 {
				keys = append(keys, vs...)
			}
		}
		if iter == 0 {
			break
		}
	}

	rtn := "ท่านตั้งค่า alert ดังนี้..."
	for _, k := range keys {
		data, err := redis.Bytes(conn.Do("GET", k))
		if err != nil {
			rtn += fmt.Sprintf("\nค่า: %s ดึงไม่ได้อ่ะ = =", k)
			continue
		}

		var p push
		if err := json.Unmarshal(data, &p); err != nil {
			rtn += fmt.Sprintf("\nค่า: %s ดึงไม่ได้อ่ะ = =", k)
			continue
		}

		price := accounting.FormatNumberFloat64(p.CheckAlert, 2, ",", ".")
		min, max := p.CheckAlert-p.CheckRange, p.CheckAlert+p.CheckRange

		rtn += fmt.Sprintf("\nค่าเงิน %s: %s (%.2f - %.2f) บาท", p.Currency, price, min, max)
	}
	return rtn, nil
}

type runRedisResponse struct {
	Source *linebot.EventSource
}

func (r runRedisResponse) Do(args ...string) (string, error) {
	if r.Source.UserID != os.Getenv("ADMIN_TOKEN") {
		return "", errors.New("ไม่ให้ทำหรอก ชิชิ")
	}

	conn := rdPool.Get()
	defer conn.Close()

	comm, coma := args[1], args[2:]
	s := make([]interface{}, len(coma))
	for i, v := range coma {
		s[i] = v
	}

	vals, err := redis.Values(conn.Do(comm, s))
	if err != nil {
		return "", errors.New("พังงงง " + comm + " " + err.Error())
	}
	resp := ""
	for _, v := range vals {
		resp += fmt.Sprint("\n", v)
	}
	return resp, nil
}
