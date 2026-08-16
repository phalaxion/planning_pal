package hub

import (
	"encoding/json"
	"log"
	"sync"
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
	room        *Room

	// send is never closed. The room, the read pump and handleError can all
	// write to it, and a closed channel would panic whichever of them lost the
	// race. Shutdown is signalled by closing done instead.
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
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
		done:        make(chan struct{}),
	}
}

// shutdown stops the client's pumps and releases its connection. It is
// idempotent and safe to call from any goroutine, including more than once —
// which matters because a dropped client is also unregistered moments later.
func (c *Client) shutdown() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			c.conn.Close()
		}
	})
}

// deliver queues a message without ever blocking. It reports false when the
// client has shut down or is too far behind to keep up, which the room treats
// as grounds for dropping it.
func (c *Client) deliver(msg []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

func (c *Client) readPump() {
	defer func() {
		if c.room != nil {
			c.room.unregister <- c
		}
		c.shutdown()
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
		case <-c.done:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
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

func (c *Client) handleError(code string, message string, fatal bool) {
	fatalStr := "No"
	if fatal {
		fatalStr = "Yes"
	}

	errMsg, _ := json.Marshal(models.Message{
		Type: "error",
		Payload: mustMarshal(map[string]string{
			"code":    code,
			"message": message,
			"fatal":   fatalStr,
		}),
	})

	// deliver never blocks, so this is safe to call from the room's goroutine.
	c.deliver(errMsg)
}
