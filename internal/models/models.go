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
	Story     string            `json:"story"`
	Votes     map[string]string `json:"votes"`
	Timestamp time.Time         `json:"timestamp"`
}

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
