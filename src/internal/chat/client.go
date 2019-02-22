//inspired by https://scotch.io/bar-talk/build-a-realtime-chat-server-with-go-and-websockets

package chat

import (
	"encoding/json"
	"bytes"
	"time"
	"log"
	"strings"
	"gopkg.in/resty.v1"
	"github.com/gorilla/websocket"
)

const (
	writeWait = 10 * time.Second
	pongWait = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	maxMessageSize = 512
	sentiment = `{"documents": [{"language":"en","id":"1", "text": "0" }]}`
	sentimentThreshold = 0.01
	warningMessage = "Please keep the converation nice..."
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type chatMessage struct {
	UserName string `json:"username"`
	Message string `json:"message"`
}

type sentimentScore struct {
	Id string 
	Score float64 
}
type sentimentReply struct {
	Documents []sentimentScore
}

type Client struct {
	hub *Hub
	conn *websocket.Conn
	send chan []byte
	cogsUrl string
}

func (c *Client) getSentiment( msg []byte ) (float64,error) {
	
	var cm chatMessage
	err := json.Unmarshal(msg, &cm)
	if err != nil {
		log.Println(err)
		return 0.00, err
	}

	req := strings.Replace(sentiment, "0", cm.Message, -1)
	resp, err := resty.R().
		SetHeader("Content-Type", "application/json").
		SetBody([]byte(req)).
		Post(c.cogsUrl)
	
	var sr sentimentReply 
	err = json.Unmarshal(resp.Body(), &sr)
	if err != nil {
		log.Println(err)
		return 0.00, err
	}
	return sr.Documents[0].Score, nil
}

func (c *Client) readMessages() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		c.hub.broadcast <- message

		score,err := c.getSentiment(message)
		if err != nil {
			log.Printf("error: %v", err)
		}

		log.Printf("Sentiment Score - %f ", score)
		if score < sentimentThreshold {
			log.Println("Sentiment fell below threshold . . .")

			nag := chatMessage{ UserName: "Adminstrator", Message: warningMessage }
			buffer, _ := json.Marshal(&nag)
			c.hub.broadcast <- buffer
		}
	} 	
}

func (c *Client) writeMessages() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}	
}