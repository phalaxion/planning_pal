package hub

import (
	"log"
	"os"
	"strings"
	"sync"

	store_type "github.com/phalaxion/planning_pal/internal/enum"
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

var GlobalHub = NewHub()

func NewHub() *Hub {
	storePath := os.Getenv("PPAL_STORE_PATH")
	if storePath == "" {
		storePath = "/var/lib/planning-pal"
	}

	storeType := os.Getenv("PPAL_STORE_TYPE")
	if storeType == "" {
		storeType = "json"
	}

	storeTypeEnum, err := store_type.ValueOf(storeType)
	if err != nil {
		log.Fatalf("Invalid store type %q", storeType)
	}

	hub := Hub{
		rooms: make(map[string]*Room),
		deck:  parseDeck(os.Getenv("PPAL_DECK")),
	}

	switch storeTypeEnum {
	case store_type.JSON:
		hub.store = store.NewJSONStore(storePath)
	case store_type.SQLITE:
		sqliteStore, err := store.NewSQLiteStore(storePath)

		if err != nil {
			log.Fatalf("Failed to initialize SQLite store: %v", err)
		}

		hub.store = sqliteStore
	}

	return &hub
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

	r := newRoom(&h.store, roomID, h.deck)
	h.rooms[roomID] = r
	go r.run()

	return r
}

func (h *Hub) Delete(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.rooms, roomID)
}
