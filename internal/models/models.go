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

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
