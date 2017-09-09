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
	case command == "setinterval":
		resp = setIntervalResponse{source}
	case command == "viewinterval":
		resp = viewIntervalResponse{source}
	case command == "removeinterval":
		resp = removeIntervalResponse{source}
	case command == "setalert":
		resp = setAlertResponse{source}
	case command == "viewalert":
		resp = viewAlertResponse{source}
	case command == "removealert":
		resp = removeAlertResponse{source}
	case command == "flushall":
		resp = flushAllResponse{source}
	default:
		resp = generalResponse{}
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

	pool := func(curr currency, i interval) error {
		members, err := redis.Strings(conn.Do("SMEMBERS", i.Key(curr.SecondaryCurrency)))
		if err != nil {
			log.Println("smembers error:", i.Key(curr.SecondaryCurrency), err)
			return err
		}

		for _, m := range members {
			data, err := redis.Bytes(conn.Do("GET", m))
			if err != nil {
				log.Println("get key error:", m, err)
				continue
			}

			var ph push
			if err := json.Unmarshal(data, &ph); err != nil {
				continue
			}

			if err := i.HandlePush(ph, curr); err != nil {
				continue
			}

			tn := time.Now()
			ph.PushedAt = &tn

			data, err = json.Marshal(ph)
			if err != nil {
				continue
			}

			if _, err := conn.Do("SET", m, data); err != nil {
				log.Println("unable to set new data:", m, err)
			}
		}

		return nil
	}

	for _, c := range curs {
		pool(c, poolingInterval{})
		pool(c, poolingAlert{})
	}

	return nil
}

type interval interface {
	Key(string) string
	HandlePush(push, currency) error
}

type poolingInterval struct{}

func (pi poolingInterval) Key(curr string) string {
	return fmt.Sprintf("interval:%s", curr)
}

func (pi poolingInterval) HandlePush(p push, curr currency) error {
	minutes := time.Duration(p.Interval) * time.Minute
	minutesAgo := time.Now().Add(-minutes).Add(-10 * time.Second)
	if p.PushedAt.After(minutesAgo) {
		return errors.New("no need to push")
	}

	msg := fmt.Sprintf("ค่าเงิน %s: %s", curr.SecondaryCurrency, accounting.FormatNumberFloat64(curr.LastPrice, 2, ",", "."))
	if _, err := bot.PushMessage(p.UserID, linebot.NewTextMessage(msg)).Do(); err != nil {
		return err
	}
	return nil
}

type poolingAlert struct{}

func (pa poolingAlert) Key(curr string) string {
	return fmt.Sprintf("alert:%s", curr)
}

func (pa poolingAlert) HandlePush(p push, curr currency) error {
	min, max := p.CheckAlert-p.CheckRange, p.CheckAlert+p.CheckRange
	if curr.LastPrice <= min || curr.LastPrice >= max {
		return errors.New("no need to push")
	}

	price := accounting.FormatNumberFloat64(curr.LastPrice, 2, ",", ".")
	msg := fmt.Sprintf("ค่าเงิน %s อยู่ในช่วง %s (%.2f - %.2f) เลยนะ ดูดีๆ", curr.SecondaryCurrency, price, min, max)
	if _, err := bot.PushMessage(p.UserID, linebot.NewTextMessage(msg)).Do(); err != nil {
		return err
	}
	return nil
}
