package hub

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/phalaxion/planning_pal/internal/models"
	"github.com/phalaxion/planning_pal/internal/store"
)

type Store interface {
	Get(room string, id string) (*models.RoundResult, error)
	// List returns a room's rounds oldest first. A positive limit returns only
	// that many of the most recent rounds; zero or less returns all of them.
	List(room string, limit int) ([]models.RoundResult, error)
	Save(room string, result models.RoundResult) error
	Delete(room string, id string) error
}

// defaultDeck is the deck used when PPAL_DECK is unset. It is the only place
// the card faces are defined — the frontend renders whatever the server sends.
var defaultDeck = []string{"1", "2", "3", "4", "5", "6", "8", "10", "12", "16", "20", "999", "?", "☕"}

// parseDeck reads a comma-separated deck from configuration, falling back to the
// default rather than leaving a deployment with no cards to play.
func parseDeck(raw string) []string {
	cards := []string{}
	for _, c := range strings.Split(raw, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cards = append(cards, c)
		}
	}

	if len(cards) == 0 {
		if strings.TrimSpace(raw) != "" {
			log.Printf("PPAL_DECK %q contained no usable cards; using the default deck", raw)
		}
		return defaultDeck
	}

	return cards
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
	store Store
	deck  []string
}

// NewHub builds a hub around an already-constructed store.
//
// The store is injected rather than built here on purpose. This used to be a
// package-level `var GlobalHub = NewHub()`, which meant merely importing this
// package created directories and could call log.Fatalf — so any test binary
// that touched it died before running a single test.
func NewHub(store Store) *Hub {
	return &Hub{
		rooms: make(map[string]*Room),
		store: store,
		deck:  parseDeck(os.Getenv("PPAL_DECK")),
	}
}

// StoreFromEnv builds the configured store. SQLite is the only backend; the
// JSON store was retired because every stored feature had to be written twice.
func StoreFromEnv() (Store, error) {
	storePath := os.Getenv("PPAL_STORE_PATH")
	if storePath == "" {
		storePath = "/var/lib/planning-pal"
	}

	switch storeType := os.Getenv("PPAL_STORE_TYPE"); storeType {
	case "", "sqlite":
		return store.NewSQLiteStore(storePath)
	case "json":
		return nil, fmt.Errorf(
			"PPAL_STORE_TYPE=json is no longer supported; set it to sqlite. "+
				"Any history in %s/results.json will not be read", storePath)
	default:
		return nil, fmt.Errorf("invalid PPAL_STORE_TYPE %q, want sqlite", storeType)
	}
}

func (h *Hub) Get(roomID string) (*Room, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	r, ok := h.rooms[roomID]

	return r, ok
}

func (h *Hub) GetOrCreateRoom(roomID string) *Room {
	if r, ok := h.Get(roomID); ok {
		return r
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if r, ok := h.rooms[roomID]; ok {
		return r
	}

	r := newRoom(h, roomID)
	h.rooms[roomID] = r
	go r.run()

	return r
}

func (h *Hub) Delete(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.rooms, roomID)
}
