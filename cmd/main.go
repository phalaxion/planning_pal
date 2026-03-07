package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/phalaxion/planning_pal/internal/hub"
)

var addr = flag.String("addr", ":8080", "http service address")

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	flag.Parse()
	mux := http.NewServeMux()

	// Static files
	fs := http.FileServer(http.Dir("frontend"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Serve lobby for the root path
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "frontend/lobby/lobby.html")
	})

	// Serve room page for any /room/{id} path
	mux.HandleFunc("/room/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "frontend/room/room.html")
	})

	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		roomID := r.URL.Query().Get("room")
		name := r.URL.Query().Get("name")
		if roomID == "" || name == "" {
			http.Error(w, "missing room or name", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade: %v", err)
			return
		}

		clientId := r.URL.Query().Get("clientId")
		client := hub.NewClient(conn, name, clientId)
		room := hub.GlobalHub.GetOrCreateRoom(roomID)
		client.Start(room)
	})

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
