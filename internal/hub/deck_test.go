package hub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseDeck(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"unset falls back to the default", "", defaultDeck},
		{"whitespace only falls back", "   ", defaultDeck},
		{"commas only falls back", " , , ", defaultDeck},
		{"fibonacci", "1,2,3,5,8,13,21,?", []string{"1", "2", "3", "5", "8", "13", "21", "?"}},
		{"spaces are trimmed", " S , M , L ", []string{"S", "M", "L"}},
		{"empty entries are dropped", "1,,2,,3", []string{"1", "2", "3"}},
		{"single card", "1", []string{"1"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseDeck(c.raw)

			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("parseDeck(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestJoiningClientReceivesTheDeck(t *testing.T) {
	r, _ := newTestRoom(t)

	// Registered by hand rather than via join(), which waits on a state update
	// and would discard the config message that precedes it.
	alice := newTestClient("a", "Alice")
	r.register <- alice

	got := awaitConfig(t, alice)

	if strings.Join(got, "|") != strings.Join(defaultDeck, "|") {
		t.Errorf("deck = %v, want %v", got, defaultDeck)
	}
}

func TestRoomServesItsConfiguredDeck(t *testing.T) {
	fs := &fakeStore{}
	var s Store = fs

	want := []string{"S", "M", "L", "XL", "?"}

	r := newRoom(&s, t.Name(), want)
	r.facilitatorGrace = 25 * time.Millisecond
	r.cleanupDelay = 25 * time.Millisecond
	go r.run()

	alice := newTestClient("a", "Alice")
	r.register <- alice

	got := awaitConfig(t, alice)

	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("deck = %v, want %v", got, want)
	}
}

// The client renders the deck on its first state update, so config has to be
// queued ahead of it or the first paint has no cards.
func TestConfigIsSentBeforeTheFirstStateUpdate(t *testing.T) {
	r, _ := newTestRoom(t)

	c := newTestClient("a", "Alice")
	r.register <- c

	// Read raw, in arrival order, rather than through the type-filtering helpers.
	first := <-c.send

	var m struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(first, &m); err != nil {
		t.Fatalf("unmarshal first message: %v", err)
	}

	if m.Type != "config" {
		t.Errorf("first message was %q, want %q", m.Type, "config")
	}
}
