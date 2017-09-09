package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/gin-gonic/gin"
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
