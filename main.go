package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/line/line-bot-sdk-go/linebot"
)

var bot *linebot.Client

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		panic("$PORT must be set")
	}

	var err error
	bot, err = linebot.New(os.Getenv("CHANNEL_SECRET"), os.Getenv("CHANNEL_TOKEN"))
	if err != nil {
		panic("cannot init line bot")
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.POST("/bot", botHandler)

	r.Run(":" + port)
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
		switch event.Type {
		case linebot.EventTypeMessage:
			if _, err := bot.ReplyMessage(event.ReplyToken, linebot.NewTextMessage("test from heroku")).Do(); err != nil {
				log.Println("reply message error:", err)
			}
		}
	}
}
