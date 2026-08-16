package models

import (
	"encoding/json"
	"time"
)

type Participant struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Vote  string `json:"vote"`
	Voted bool   `json:"voted"`
}

type RoundResult struct {
	ID          string            `json:"id"`
	Story       string            `json:"story"`
	Timestamp   time.Time         `json:"timestamp"`
	AverageVote float64           `json:"average_vote"`
	Votes       map[string]string `json:"votes"`
}

// Queue item statuses. Only pending items are loaded into a room; done ones
// stay in the store as a record and never come back.
const (
	QueuePending = "pending"
	QueueDone    = "done"
)

// QueueItem is something the team intends to estimate, captured ahead of the
// session. Unlike a Room, it outlives the session it was captured for.
type QueueItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Notes     string    `json:"notes"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
