package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
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

	p := pooling{}
	go p.Run()

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

	var resp textResponse
	switch {
	case command == "help":
		resp = helpResponse{}
	case strings.HasPrefix(command, "curr"):
		resp = currentResponse{}
	case strings.HasPrefix(command, "setinterval"):
		resp = setIntervalResponse{source}
	case command == "viewinterval":
		resp = viewIntervalResponse{source}
	case command == "flushall":
		resp = flushAllResponse{source}
	default:
		resp = generalResponse{}

		//key := fmt.Sprintf("%s:push", strings.ToLower(args[1]))
		//fmt.Println("key:", key)
		//
		//members, err := redis.Strings(conn.Do("SMEMBERS", key))
		//if err != nil {
		//	rtn = "redis พังอ่ะ " + err.Error()
		//	goto ex
		//}
		//fmt.Println("members:", members)
		//
		//for _, m := range members {
		//	rtn = m + "\n"
		//
		//	vs, err := redis.Values(conn.Do("HMGET", m, "interval", "last_push"))
		//	if err != nil {
		//		rtn += "error จ้าาาา " + err.Error()
		//		continue
		//	}
		//	fmt.Println(vs)
		//}
	}

	rtn, err := resp.Do(args...)
	if err != nil {
		return linebot.NewTextMessage(err.Error())
	}
	return linebot.NewTextMessage(rtn)
}

func getBXCurrency() (currencies, error) {
	resp, err := http.Get(bxAPI)
	if err != nil {
		return nil, err
	}

	var p interface{}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var curs currencies
	for _, c := range p.(map[string]interface{}) {
		curs = append(curs, parseCurrency(c.(map[string]interface{})))
	}
	return curs, nil
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
		PrimaryCurrency:   strings.ToLower(data["primary_currency"].(string)),
		SecondaryCurrency: strings.ToLower(data["secondary_currency"].(string)),
		Change:            data["change"].(float64),
		LastPrice:         data["last_price"].(float64),
		Volume24Hours:     data["volume_24hours"].(float64),
	}
}

type currencies []currency

func (curs currencies) GetByName(name string) currency {
	for _, c := range curs {
		if c.SecondaryCurrency == strings.ToLower(name) {
			return c
		}
	}
	return currency{}
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

type pooling struct{}

func (p pooling) Run() {
	for range time.Tick(5 * time.Minute) {
		if err := p.sending(); err != nil {
			log.Println("pooling error:", err)
			continue
		}
	}
}

func (p pooling) sending() error {
	curs, err := getBXCurrency()
	if err != nil {
		return errors.New("unable to get bx currency: " + err.Error())
	}

	conn := rdPool.Get()
	defer conn.Close()

	for _, c := range curs {
		key := fmt.Sprintf("push:%s", c.SecondaryCurrency)
		members, err := redis.Strings(conn.Do("SMEMBERS", key))
		if err != nil {
			continue
		}

		for _, m := range members {
			data, err := redis.Bytes(conn.Do("GET", m))
			if err != nil {
				continue
			}

			var p pushInterval
			if err := json.Unmarshal(data, &p); err != nil {
				continue
			}

			minutes := time.Duration(p.Interval) * time.Minute
			minutesAgo := time.Now().Add(-minutes)
			if p.PushedAt.Before(minutesAgo) {
				msg := fmt.Sprintf("ค่าเงิน %s: %s", p.Currency, accounting.FormatNumberFloat64(c.LastPrice, 2, ",", "."))
				if _, err := bot.PushMessage(p.UserID, linebot.NewTextMessage(msg)).Do(); err != nil {
					log.Println("push message error:", p.UserID, err)
					continue
				}

				tn := time.Now()
				p.PushedAt = &tn

				data, err := json.Marshal(p)
				if err != nil {
					log.Println("unable to marshal json:", err)
					continue
				}

				if _, err := conn.Do("SET", m, data); err != nil {
					log.Println("unable to set new data:", m, err)
				}
			}
		}
	}

	return nil
}
