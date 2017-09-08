package main

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

//import "github.com/line/line-bot-sdk-go/linebot"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		panic("$PORT must be set")
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.GET("/bot", botHandler)

	r.Run(":" + port)
}

func botHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
