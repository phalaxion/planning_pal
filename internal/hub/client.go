package hub

import (
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/phalaxion/planning_pal/internal/models"
)

const (
	writeWait = 10 * time.Second
	pongWait  = 60 * time.Second
)

type Client struct {
	id          string
	name        string
	participant *models.Participant
	conn        *websocket.Conn
	send        chan []byte
	room        *Room
}

func NewClient(conn *websocket.Conn, name string, id string) *Client {
	if id == "" {
		id = uuid.NewString()
	}
	p := &models.Participant{ID: id, Name: name, Vote: "", Voted: false}
	return &Client{
		id:          id,
		name:        name,
		participant: p,
		conn:        conn,
		send:        make(chan []byte, 32),
	}
}

func (c *Client) readPump() {
	defer func() {
		if c.room != nil {
			c.room.unregister <- c
		}
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("readPump error: %v", err)
			}
			break
		}
		var m models.Message
		if err := json.Unmarshal(data, &m); err != nil {
			log.Printf("invalid message: %v", err)
			continue
		}
		if c.room != nil {
			c.room.inbound <- inboundMessage{client: c, msg: m}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker((pongWait * 9) / 10)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(msg)
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

// Start registers the client with a room and starts its read/write pumps.
func (c *Client) Start(r *Room) {
	c.room = r
	r.register <- c
	go c.writePump()
	go c.readPump()
}
