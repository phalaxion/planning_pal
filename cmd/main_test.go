package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phalaxion/planning_pal/internal/hub"
	"github.com/phalaxion/planning_pal/internal/store"
)

// testMux wires the real mux to a real store in a temp directory. The store is
// injected rather than global, so each test gets its own and none of them touch
// the configured data directory.
func testMux(t *testing.T) *http.ServeMux {
	t.Helper()

	s, err := store.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.DB.Close() })

	return newMux("../frontend", hub.NewHub(s))
}

// Pages cannot carry a version in their own URL, so they have to revalidate —
// that is what gets a browser the current assetVersion. Assets behind those
// versioned URLs are cached hard instead.
func TestPagesRevalidateAndAssetsAreCachedHard(t *testing.T) {
	srv := httptest.NewServer(testMux(t))
	t.Cleanup(srv.Close)

	cases := []struct {
		path string
		want string
	}{
		{"/", "no-cache"},
		{"/room/ABC123", "no-cache"},
		{"/admin/ABC123", "no-cache"},
		{"/static/room/room.js", "public, max-age=31536000, immutable"},
		{"/static/core/Connection.js", "public, max-age=31536000, immutable"},
		{"/static/room/room.css", "public, max-age=31536000, immutable"},
	}

	for _, c := range cases {
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", c.path, resp.StatusCode)
			continue
		}

		if got := resp.Header.Get("Cache-Control"); got != c.want {
			t.Errorf("GET %s: Cache-Control = %q, want %q", c.path, got, c.want)
		}
	}
}

// frontendFingerprint hashes the content of every frontend file.
func frontendFingerprint(t *testing.T, dir string) string {
	t.Helper()

	h := sha256.New()

	// WalkDir visits lexically, so the same tree always hashes the same way.
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		fmt.Fprintf(h, "%s\n", filepath.ToSlash(rel))
		h.Write(body)

		return nil
	})
	if err != nil {
		t.Fatalf("fingerprint %s: %v", dir, err)
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Assets are immutable and cached for a year, so a changed file only reaches a
// browser if assetVersion changed too. Forgetting is silent in production and
// unrecoverable for a year, so it fails here instead.
func TestAssetVersionMatchesTheFrontend(t *testing.T) {
	got := frontendFingerprint(t, "../frontend")

	if got != assetFingerprint {
		t.Fatalf(`the frontend changed but assetVersion was not bumped.

Assets are served immutable, so clients will keep the old files until the URL
changes. In cmd/main.go, bump assetVersion (currently %q) and set:

    assetFingerprint = %q
`, assetVersion, got)
	}
}

func TestPagesCarryAStampedAssetVersion(t *testing.T) {
	srv := httptest.NewServer(testMux(t))
	t.Cleanup(srv.Close)

	for _, p := range []string{"/", "/room/ABC123", "/admin/ABC123"} {
		body := get(t, srv.URL+p)

		if strings.Contains(body, assetVersionToken) {
			t.Errorf("GET %s: placeholder %s was left unsubstituted", p, assetVersionToken)
		}
		if !strings.Contains(body, ".js?v="+assetVersion) {
			t.Errorf("GET %s: no versioned script URL in the page", p)
		}
		if !strings.Contains(body, ".css?v="+assetVersion) {
			t.Errorf("GET %s: no versioned stylesheet URL in the page", p)
		}
	}
}

// Versioned URLs must still resolve — the query string is a cache key, and the
// file server has to ignore it.
func TestVersionedAssetURLsStillServeTheFile(t *testing.T) {
	srv := httptest.NewServer(testMux(t))
	t.Cleanup(srv.Close)

	body := get(t, srv.URL+"/static/room/room.js?v="+assetVersion)

	if !strings.Contains(body, "history_update") {
		t.Error("versioned room.js did not serve the expected file")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}

	return string(body)
}

// A lowercase room code used to open a second, empty room that looked exactly
// like the right one, so the page URL is canonicalised before anything renders.
func TestRoomCodesAreCanonicalisedToUppercase(t *testing.T) {
	srv := httptest.NewServer(testMux(t))
	t.Cleanup(srv.Close)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	cases := []struct {
		path         string
		wantRedirect bool
		wantLocation string
	}{
		{"/room/abc123", true, "/room/ABC123"},
		{"/room/AbC123", true, "/room/ABC123"},
		{"/room/abc123?name=Alice", true, "/room/ABC123?name=Alice"},
		{"/admin/abc123", true, "/admin/ABC123"},
		{"/room/ABC123", false, ""},
		{"/admin/ABC123", false, ""},
	}

	for _, c := range cases {
		resp, err := client.Get(srv.URL + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		resp.Body.Close()

		if !c.wantRedirect {
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s: status %d, want 200 (already canonical)", c.path, resp.StatusCode)
			}
			continue
		}

		if resp.StatusCode != http.StatusFound {
			t.Errorf("GET %s: status %d, want 302", c.path, resp.StatusCode)
			continue
		}
		if got := resp.Header.Get("Location"); got != c.wantLocation {
			t.Errorf("GET %s: Location = %q, want %q", c.path, got, c.wantLocation)
		}
	}
}

func TestWebsocketRequiresRoomAndName(t *testing.T) {
	srv := httptest.NewServer(testMux(t))
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
