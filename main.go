package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/line/line-bot-sdk-go/linebot"
)

var (
	bot   *linebot.Client
	bxAPI = "https://bx.in.th/api/"
)

func main() {
	var err error
	bot, err = linebot.New(os.Getenv("CHANNEL_SECRET"), os.Getenv("CHANNEL_TOKEN"))
	if err != nil {
		panic("cannot init line bot")
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
				if _, err := bot.ReplyMessage(event.ReplyToken, lineTextResponse(msg.Text)).Do(); err != nil {
					log.Println("reply message error:", err)
				}
			}
		}
	}
}

func lineTextResponse(msg string) *linebot.TextMessage {
	args := strings.Split(msg, " ")
	command := strings.ToLower(args[0])
	rtn := "ยังตอบไม่ได้อ่ะ เสียใจ T^T"

	switch {
	case strings.HasPrefix(command, "help"):
		rtn = "ทำแบบนี้ๆ\n"
		rtn += "1. curr ${currency}"
	case strings.HasPrefix(command, "curr"):
		curr, err := getBXCurrency(args[1])
		if err != nil {
			rtn = "error เบย: " + err.Error()
		}
		rtn = fmt.Sprint("ค่าเงิน ", args[1], ": ", curr.LastPrice)
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
