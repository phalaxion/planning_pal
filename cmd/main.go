package main

import (
	"bytes"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	"github.com/phalaxion/planning_pal/internal/hub"
)

var addr = flag.String("addr", ":8080", "http service address")

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// noCache forces the browser to revalidate before reusing a cached asset.
//
// "no-cache" does not mean "do not store" — the browser still caches, it just
// has to ask whether its copy is current, which is a cheap 304 when nothing has
// changed. Without it there is no Cache-Control header at all, and browsers
// apply heuristic freshness: they will happily reuse a stale room.js for hours.
//
// That matters because the JS and the websocket protocol it speaks are deployed
// together but cached separately. A browser running yesterday's room.js against
// today's server does not fail loudly; it silently renders the wrong thing.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// assetVersion is stamped into every asset URL in the HTML pages, so a new
// deploy cannot be served from a cache keyed on the old URL. Bump it by hand to
// force every client to refetch the frontend.
//
// This is belt and braces: Cache-Control above is what actually guarantees a
// fresh frontend, and this only matters if something between us and the browser
// ignores it. A forgotten bump is therefore harmless, which is why one constant
// is the right amount of machinery here.
const assetVersion = "1"

// assetVersionToken is the placeholder the HTML pages carry on every asset URL.
const assetVersionToken = "__ASSET_VERSION__"

// servePage serves a single HTML file with the asset version stamped in.
func servePage(staticPath, page string) http.Handler {
	return noCache(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(staticPath + page)
		if err != nil {
			log.Printf("serving %s: %v", page, err)
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}

		body = bytes.ReplaceAll(body, []byte(assetVersionToken), []byte(assetVersion))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(body)
	}))
}

func newMux(staticPath string) *http.ServeMux {
	mux := http.NewServeMux()

	// Static files
	fs := http.FileServer(http.Dir(staticPath))
	mux.Handle("/static/", noCache(http.StripPrefix("/static/", fs)))

	// Serve lobby for the root path
	mux.Handle("/", servePage(staticPath, "/lobby/lobby.html"))

	// Serve room page for any /room/{id} path
	mux.Handle("/room/", servePage(staticPath, "/room/room.html"))

	// Serve admin page for any /admin/{id} path
	mux.Handle("/admin/", servePage(staticPath, "/admin/admin.html"))

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

	return mux
}

func main() {
	flag.Parse()

	staticPath := os.Getenv("STATIC_PATH")
	if staticPath == "" {
		staticPath = "frontend" // fallback for local dev
	}

	mux := newMux(staticPath)

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
