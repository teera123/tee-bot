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
)

type textResponse interface {
	Do(args ...string) (string, error)
}

type pushInterval struct {
	UserID   string     `json:"user_id"`
	Currency string     `json:"currency"`
	Interval int        `json:"interval"`
	PushedAt *time.Time `json:"pushed_at"`
}

type generalResponse struct{}

func (g generalResponse) Do(args ...string) (string, error) {
	return "ตอบไม่ได้อ่ะ เสียใจ T^T", nil
}

type helpResponse struct{}

func (h helpResponse) Do(args ...string) (string, error) {
	rtn := "ทำแบบนี้ๆ\n"
	rtn += "1. curr ${currency}\n"
	rtn += "2. setinterval ${currency} ${minutes}\n"
	rtn += "3. viewinterval"

	return rtn, nil
}

type currentResponse struct{}

func (c currentResponse) Do(args ...string) (string, error) {
	curr, err := getBXCurrency(args[1])
	if err != nil {
		return "", errors.New("error เบย " + err.Error())
	}
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
	if ti < 5 {
		return "", errors.New("ตอนนี้ได้ต่ำสุดแค่ 5 นาทีก่อนนะ")
	}

	hkey := fmt.Sprintf("%s:%s:interval", s.Source.UserID, curr)
	skey := fmt.Sprintf("push:%s", curr)

	tn := time.Now()
	p := pushInterval{
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

	rtn := "ท่านตั้งค่า interval ดังนี้...\n"
	for _, k := range keys {
		data, err := redis.Bytes(conn.Do("GET", k))
		if err != nil {
			rtn += fmt.Sprintf("ค่า: %s ดึงไม่ได้อ่ะ = =", k)
			continue
		}

		var p pushInterval
		if err := json.Unmarshal(data, &p); err != nil {
			rtn += fmt.Sprintf("ค่า: %s ดึงไม่ได้อ่ะ = =", k)
			continue
		}
		rtn += fmt.Sprintf("ค่าเงิน: %s\n", p.Currency)
		rtn += fmt.Sprintf("ยิงเมื่อ: %d นาที\n", p.Interval)
		rtn += "============\n"
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
