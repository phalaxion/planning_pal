package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/phalaxion/planning_pal/internal/models"
)

type inboundMessage struct {
	client *Client
	msg    models.Message
}

type Room struct {
	ID                 string
	participants       map[string]*Client
	phase              string
	story              string
	facilitatorID      string
	lastFacilitator    string
	facilitatorTimer   *time.Timer
	facilitatorTimerCh chan struct{}
	cleanupTimer       *time.Timer
	cleanupTimerCh     chan struct{}
	history            []models.RoundResult

	register   chan *Client
	unregister chan *Client
	inbound    chan inboundMessage
}

func newRoom(id string) *Room {
	r := &Room{
		ID:                 id,
		participants:       make(map[string]*Client),
		phase:              "voting",
		story:              "",
		facilitatorTimerCh: make(chan struct{}, 1),
		cleanupTimerCh:     make(chan struct{}, 1),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		inbound:            make(chan inboundMessage, 16),
	}

	// Attempt to load the history for this room
	if err := r.loadHistory(); err != nil {
		log.Printf("room %s: loadHistory error: %v", id, err)
	}

	return r
}

func (r *Room) historyFilePath() string {
	base := "data/rooms"
	return filepath.Join(base, r.ID+".json")
}

func (r *Room) loadHistory() error {
	path := r.historyFilePath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var h []models.RoundResult
	if err := json.Unmarshal(data, &h); err != nil {
		return err
	}

	r.history = h

	return nil
}

func (r *Room) persistHistory() error {
	path := r.historyFilePath()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r.history, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func (r *Room) signal(ch chan<- struct{}) bool {
	select {
	case ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *Room) run() {
	for {
	roomAction:
		select {
		case <-r.facilitatorTimerCh:
			r.lastFacilitator = ""
			r.facilitatorTimer = nil

			log.Printf("room %s: cleared facilitator", r.ID)

			if len(r.participants) > 0 {
				for _, c := range r.participants {
					r.facilitatorID = c.id
					break
				}
			}

			r.broadcastStateToAll()

		case <-r.cleanupTimerCh:
			if r.facilitatorTimer != nil {
				r.facilitatorTimer.Stop()
			}

			GlobalHub.Delete(r.ID)

			log.Printf("room %s: closed room", r.ID)

		case c := <-r.register:
			log.Printf("room %s: register client=%s name=%s (before) count=%d", r.ID, c.id, c.name, len(r.participants))

			if r.cleanupTimer != nil {
				log.Printf("room %s: stopped cleanup timer", r.ID)

				r.cleanupTimer.Stop()
				r.cleanupTimer = nil
			}

			for _, existing := range r.participants {
				if existing.id != c.id && existing.name == c.name {
					c.handleError("name_taken", fmt.Sprintf("'%s' is already taken in the room. Please choose a different name.", c.name), true)
					break roomAction
				}
			}

			// if an existing client with same id exists, close its connection (treat as reconnect)
			if existing, ok := r.participants[c.id]; ok && existing != c {
				existing.conn.Close()
			}

			r.participants[c.id] = c

			if r.lastFacilitator == c.id && r.facilitatorTimer != nil {
				// if this client was the last facilitator and a timer is pending, restore facilitator
				r.facilitatorTimer.Stop()
				r.facilitatorTimer = nil
				r.facilitatorID = c.id
				r.lastFacilitator = ""
			} else if r.facilitatorID == "" {
				// Otherwsie if we do not have a facilitator, assign to the new client (first-come-first-serve)
				r.facilitatorID = c.id
			}

			r.broadcastStateToAll()

			log.Printf("room %s: registered client=%s name=%s (after) count=%d", r.ID, c.id, c.name, len(r.participants))

		case c := <-r.unregister:
			// If this client was never registered (e.g. rejected for name_taken), just close and skip
			if _, ok := r.participants[c.id]; !ok {
				close(c.send)
				continue
			}

			// ignore stale unregisters (happens when an old connection is closed after a reconnect)
			if cur, ok := r.participants[c.id]; ok && cur != c {
				// stale unregister; do nothing
				continue
			}

			log.Printf("room %s: unregister client=%s name=%s (before) count=%d", r.ID, c.id, c.name, len(r.participants))

			delete(r.participants, c.id)
			close(c.send)

			// If this was the facilitator make a note and start a timer to promote another after a grace period (to allow for quick rejoins without losing facilitator role)
			if r.facilitatorID == c.id {
				r.lastFacilitator = c.id
				r.facilitatorID = ""
				if r.facilitatorTimer != nil {
					r.facilitatorTimer.Stop()
				}

				log.Printf("room %s: facilitator unregistered, starting reassign counter", r.ID)

				r.facilitatorTimer = time.AfterFunc(5*time.Second, func() {
					r.signal(r.facilitatorTimerCh)
				})
			}

			// If this was the last participant, schedule a cleanup of the room after a short delay (to allow for quick rejoins without losing state)
			if len(r.participants) == 0 {
				if r.cleanupTimer != nil {
					r.cleanupTimer.Stop()
				}

				log.Printf("room %s: no participants, started cleanup counter", r.ID)

				r.cleanupTimer = time.AfterFunc(5*time.Second, func() {
					r.signal(r.cleanupTimerCh)
				})
			}

			r.broadcastStateToAll()

			log.Printf("room %s: unregistered client=%s name=%s (after) count=%d", r.ID, c.id, c.name, len(r.participants))

		case im := <-r.inbound:
			r.handleClientMessage(im.client, im.msg)
		}
	}
}

func (r *Room) handleClientMessage(c *Client, m models.Message) {
	switch m.Type {
	case "vote":
		var payload struct {
			Card string `json:"card"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			log.Printf("invalid vote payload: %v", err)
			c.handleError("invalid_vote", "Invalid vote payload provided", false)
			return
		}

		if p := r.getParticipantByID(c.id); p != nil {
			p.Vote = payload.Card
		}
		r.broadcastStateToAll()
	case "reveal":
		r.phase = "revealed"
		r.broadcastStateToAll()
	case "new_round":
		var payload struct {
			Story string `json:"story"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			log.Printf("invalid new_round payload: %v", err)
			c.handleError("invalid_new_round", "Invalid new_round payload provided", false)
			return
		}

		votes := make(map[string]string)
		for _, p := range r.participants {
			if p.participant != nil {
				votes[p.participant.Name] = p.participant.Vote
				p.participant.Vote = ""
			}
		}

		r.history = append(r.history, models.RoundResult{Story: r.story, Votes: votes, Timestamp: time.Now().UTC()})

		if err := r.persistHistory(); err != nil {
			log.Printf("room %s: persistHistory error: %v", r.ID, err)
			c.handleError("history_failed", fmt.Sprintf("Failed to save round history: %v", err), false)
		}

		r.story = payload.Story
		r.phase = "voting"
		r.broadcastStateToAll()
	case "set_story":
		var payload struct {
			Story string `json:"story"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return
		}

		r.story = payload.Story
		r.broadcastStateToAll()
	}
}

func (r *Room) getParticipantByID(id string) *models.Participant {
	if c, ok := r.participants[id]; ok {
		return c.participant
	}
	return nil
}

// broadcastStateToAll sends a tailored state_update to each connected client,
// masking other participants' votes during the voting phase.
func (r *Room) broadcastStateToAll() {
	clients := make([]*Client, 0, len(r.participants))
	for _, c := range r.participants {
		clients = append(clients, c)
	}

	for _, recipient := range r.participants {
		parts := make([]*models.Participant, 0, len(clients))
		for _, p := range clients {
			copyP := *p.participant
			// set voted flag based on actual stored vote
			copyP.Voted = p.participant.Vote != ""
			if r.phase == "voting" && recipient.id != p.id {
				// mask the value for other viewers; preserve empty for not-yet-voted
				if p.participant.Vote != "" {
					copyP.Vote = "hidden"
				} else {
					copyP.Vote = ""
				}
			}
			parts = append(parts, &copyP)
		}

		payload := map[string]interface{}{
			"roomId":        r.ID,
			"phase":         r.phase,
			"story":         r.story,
			"facilitatorId": r.facilitatorID,
			"participants":  parts,
			"history":       r.history,
			"youId":         recipient.id,
		}
		b, _ := json.Marshal(models.Message{Type: "state_update", Payload: mustMarshal(payload)})

		select {
		case recipient.send <- b:
		default:
			// if the client's send channel is full, drop it and close
			close(recipient.send)
		}
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
