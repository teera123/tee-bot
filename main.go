package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/gin-gonic/gin"
	"github.com/leekchan/accounting"
	"github.com/line/line-bot-sdk-go/linebot"
)

var (
	bot    *linebot.Client
	rdPool *redis.Pool
	bxAPI  = "https://bx.in.th/api/"
)

func main() {
	var err error
	bot, err = linebot.New(os.Getenv("CHANNEL_SECRET"), os.Getenv("CHANNEL_TOKEN"))
	if err != nil {
		panic("cannot init line bot")
	}

	rdPool, err = createRedisPool()
	if err != nil {
		panic("cannot connect redis")
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.POST("/bot", botHandler)

	r.Run(":" + os.Getenv("PORT"))
}

func botHandler(c *gin.Context) {
	events, err := bot.ParseRequest(c.Request)
	if err != nil {
		log.Println("line bot parse request error:", err)

		if err == linebot.ErrInvalidSignature {
			c.Writer.WriteHeader(http.StatusBadRequest)
		} else {
			c.Writer.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	for _, event := range events {
		if event.Type == linebot.EventTypeMessage {
			switch msg := event.Message.(type) {
			case *linebot.TextMessage:
				reply := lineTextResponse(msg.Text, event.Source)
				if _, err := bot.ReplyMessage(event.ReplyToken, reply).Do(); err != nil {
					log.Println("reply message error:", err)
				}
			}
		}
	}
}

func lineTextResponse(msg string, source *linebot.EventSource) *linebot.TextMessage {
	args := strings.Split(msg, " ")
	command := strings.ToLower(args[0])
	rtn := "ตอบไม่ได้อ่ะ เสียใจ T^T"

	switch {
	case strings.HasPrefix(command, "help"):
		rtn = "ทำแบบนี้ๆ\n"
		rtn += "1. curr ${currency}"
		rtn += "2. setinterval ${currency} ${minutes}"
	case strings.HasPrefix(command, "curr"):
		curr, err := getBXCurrency(args[1])
		if err != nil {
			rtn = "error เบย: " + err.Error()
		}
		rtn = fmt.Sprint("ค่าเงิน ", args[1], ": ", accounting.FormatNumberFloat64(curr.LastPrice, 2, ",", "."))
	case strings.HasPrefix(command, "setinterval"):
		t, err := strconv.Atoi(args[2])
		if err != nil {
			rtn = "ส่งเวลาเป็นตัวเลขด้วยจ้าาาา"
			goto ex
		}

		curr := strings.ToLower(args[1])
		conn := rdPool.Get()
		defer conn.Close()

		hkey := fmt.Sprintf("%s:%s", curr, source.UserID)
		skey := fmt.Sprintf("%s:push", curr)

		conn.Send("MULTI")
		conn.Send("HMSET", hkey, "interval", t, "last_push", time.Now())
		conn.Send("SADD", skey, hkey)
		reply, err := conn.Do("EXEC")
		if err != nil {
			rtn = "redis พังอ่ะ " + err.Error()
			goto ex
		}
		fmt.Println("reply:", reply, skey, hkey)

		rtn = "ตั้งค่าเรียบร้อยคร๊าบบบบบ DED"
	case strings.HasPrefix(command, "viewinterval"):
		conn := rdPool.Get()
		defer conn.Close()

		key := fmt.Sprintf("%s:push", strings.ToLower(args[1]))
		fmt.Println("key:", key)

		members, err := redis.Strings(conn.Do("SMEMBERS", key))
		if err != nil {
			rtn = "redis พังอ่ะ " + err.Error()
			goto ex
		}
		fmt.Println("members:", members)

		for _, m := range members {
			rtn += m + "\n"

			vs, err := redis.Values(conn.Do("HMGET", m, "interval", "last_push"))
			if err != nil {
				rtn += "error จ้าาาา " + err.Error()
				continue
			}
			fmt.Println(vs)
		}
	}

ex:
	return linebot.NewTextMessage(rtn)
}

func getBXCurrency(name string) (currency, error) {
	resp, err := http.Get(bxAPI)
	if err != nil {
		return currency{}, err
	}

	var p interface{}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return currency{}, err
	}
	defer resp.Body.Close()

	var curs []currency
	for _, c := range p.(map[string]interface{}) {
		curs = append(curs, parseCurrency(c.(map[string]interface{})))
	}

	for _, c := range curs {
		if strings.ToLower(c.SecondaryCurrency) == strings.ToLower(name) {
			return c, nil
		}
	}
	return currency{}, nil
}

type currency struct {
	PrimaryCurrency   string
	SecondaryCurrency string
	Change            float64
	LastPrice         float64
	Volume24Hours     float64
}

func parseCurrency(data map[string]interface{}) currency {
	return currency{
		PrimaryCurrency:   data["primary_currency"].(string),
		SecondaryCurrency: data["secondary_currency"].(string),
		Change:            data["change"].(float64),
		LastPrice:         data["last_price"].(float64),
		Volume24Hours:     data["volume_24hours"].(float64),
	}
}

// parseURL in the form of redis://h:<pwd>@ec2-23-23-129-214.compute-1.amazonaws.com:25219
// and return the host and password
func parseURL(us string) (string, string, error) {
	u, err := url.Parse(us)
	if err != nil {
		return "", "", err
	}

	password := ""
	if u.User != nil {
		password, _ = u.User.Password()
	}

	host := "localhost"
	if u.Host != "" {
		host = u.Host
	}
	return host, password, nil
}

func createRedisPool() (*redis.Pool, error) {
	h, p, err := parseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		return nil, err
	}
	pool := &redis.Pool{
		MaxIdle:     5,
		IdleTimeout: 5 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", h)
			if err != nil {
				return nil, err
			}
			if p != "" {
				if _, err := c.Do("AUTH", p); err != nil {
					c.Close()
					return nil, err
				}
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}
	return pool, nil
}
