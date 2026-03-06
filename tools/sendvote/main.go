package main

import (
	"flag"
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	server := flag.String("s", "localhost:8080", "server addr")
	room := flag.String("room", "TEST", "room id")
	name := flag.String("name", "sender", "name")
	card := flag.String("card", "5", "card value")
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: *server, Path: "/ws", RawQuery: "room=" + *room + "&name=" + *name}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer c.Close()

	msg := map[string]interface{}{"type": "vote", "payload": map[string]string{"card": *card}}
	if err := c.WriteJSON(msg); err != nil {
		log.Fatalf("write: %v", err)
	}
	// give server time to broadcast
	time.Sleep(200 * time.Millisecond)
}
