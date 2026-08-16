package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The frontend and the websocket protocol it speaks are deployed together but
// cached separately, so a browser reusing an old room.js against a new server
// misbehaves silently. Every asset must therefore be revalidated, not merely
// cached with a Last-Modified date and left to heuristic freshness.
func TestAssetsAreRevalidated(t *testing.T) {
	srv := httptest.NewServer(newMux("../frontend"))
	t.Cleanup(srv.Close)

	paths := []string{
		"/",
		"/room/ABC123",
		"/admin/ABC123",
		"/static/room/room.js",
		"/static/core/Connection.js",
		"/static/room/room.css",
	}

	for _, p := range paths {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", p, resp.StatusCode)
			continue
		}

		if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control = %q, want %q", p, got, "no-cache")
		}
	}
}

func TestWebsocketRequiresRoomAndName(t *testing.T) {
	srv := httptest.NewServer(newMux("../frontend"))
	t.Cleanup(srv.Close)

	cases := []struct {
		query string
		want  int
	}{
		{"", http.StatusBadRequest},
		{"?room=ABC123", http.StatusBadRequest},
		{"?name=Alice", http.StatusBadRequest},
	}

	for _, c := range cases {
		resp, err := http.Get(srv.URL + "/ws" + c.query)
		if err != nil {
			t.Fatalf("GET /ws%s: %v", c.query, err)
		}
		resp.Body.Close()

		if resp.StatusCode != c.want {
			t.Errorf("GET /ws%s: status %d, want %d", c.query, resp.StatusCode, c.want)
		}
	}
}
