package hub

import (
	"sync"
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

var GlobalHub = NewHub()

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]*Room)}
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
	// double-check
	if r, ok := h.rooms[roomID]; ok {
		return r
	}
	r := newRoom(roomID)
	h.rooms[roomID] = r
	go r.run()
	return r
}

func (h *Hub) Delete(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, roomID)
}
