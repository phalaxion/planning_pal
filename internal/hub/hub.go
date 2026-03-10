package hub

import (
	"log"
	"os"
	"sync"

	store_type "github.com/phalaxion/planning_pal/internal/enum"
	"github.com/phalaxion/planning_pal/internal/models"
	"github.com/phalaxion/planning_pal/internal/store"
)

type Store interface {
	Get(room string, id string) (*models.RoundResult, error)
	List(room string) ([]models.RoundResult, error)
	Save(room string, result models.RoundResult) error
	Delete(room string, id string) error
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
	store Store
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

	r := newRoom(&h.store, roomID)
	h.rooms[roomID] = r
	go r.run()

	return r
}

func (h *Hub) Delete(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.rooms, roomID)
}
