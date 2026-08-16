package main

import (
	"bytes"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/phalaxion/planning_pal/internal/hub"
)

var addr = flag.String("addr", ":8080", "http service address")

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// noCache forces the browser to revalidate before reusing a cached page.
//
// "no-cache" does not mean "do not store" — the browser still caches, it just
// has to ask whether its copy is current, which is a cheap 304 when nothing has
// changed.
//
// This is applied to the HTML pages, and it is what makes the whole scheme work:
// a page cannot carry a version in its own URL, so it has to revalidate. Once it
// does, it hands out asset URLs stamped with the current assetVersion.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// immutableCache lets assets be cached hard, because their URLs are versioned.
//
// This is safe only for as long as assetVersion actually changes whenever the
// frontend does — a stale asset here is cached for a year with no way to
// self-heal. TestAssetVersionMatchesTheFrontend is the guard: it fails if the
// frontend changes without a bump.
func immutableCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}

// assetVersion is stamped into every asset URL in the HTML pages.
//
// Assets are served immutable and cached for a year, so this is the *only*
// thing that gets a changed file to a browser. Bump it whenever anything under
// the frontend directory changes. Forgetting is not harmless: clients would go
// on running the old JS against a new server, silently, with no self-heal.
//
// assetFingerprint records the frontend this version describes.
// TestAssetVersionMatchesTheFrontend fails when the two drift apart, so the
// mistake surfaces in the test suite rather than in production.
const (
	assetVersion     = "2"
	assetFingerprint = "d607e39a114dfcd3"
)

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

// canonicaliseRoom redirects /room/abc123 to /room/ABC123.
//
// Room codes are generated uppercase and the lobby uppercases what you type,
// but nothing else did — so a hand-typed URL, or a chat client that lowercased
// a link, silently opened a *second* room with its own history that looked
// exactly like the right one.
func canonicaliseRoom(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, prefix)
		upper := strings.ToUpper(id)

		if id != upper {
			target := *r.URL
			target.Path = prefix + upper
			http.Redirect(w, r, target.String(), http.StatusFound)
			return
		}

		h.ServeHTTP(w, r)
	})
}

func newMux(staticPath string, rooms *hub.Hub) *http.ServeMux {
	mux := http.NewServeMux()

	// Static files
	fs := http.FileServer(http.Dir(staticPath))
	mux.Handle("/static/", immutableCache(http.StripPrefix("/static/", fs)))

	// Serve lobby for the root path
	mux.Handle("/", servePage(staticPath, "/lobby/lobby.html"))

	// Serve room page for any /room/{id} path
	mux.Handle("/room/", canonicaliseRoom("/room/", servePage(staticPath, "/room/room.html")))

	// Serve admin page for any /admin/{id} path
	mux.Handle("/admin/", canonicaliseRoom("/admin/", servePage(staticPath, "/admin/admin.html")))

	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Uppercased here too, not just on the page redirect: this is the value
		// rooms and stored history are keyed on, and tools connect straight to
		// the socket without ever loading a page.
		roomID := strings.ToUpper(r.URL.Query().Get("room"))
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
		room := rooms.GetOrCreateRoom(roomID)
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

	store, err := hub.StoreFromEnv()
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	mux := newMux(staticPath, hub.NewHub(store))

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
