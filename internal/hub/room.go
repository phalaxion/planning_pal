package hub

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	participantsOrder  []string
	phase              string
	story              string
	facilitatorID      string
	lastFacilitator    string
	facilitatorTimer   *time.Timer
	facilitatorTimerCh chan struct{}
	history            []models.RoundResult

	register   chan *Client
	unregister chan *Client
	inbound    chan inboundMessage
}

func newRoom(id string) *Room {
	r := &Room{
		ID:                 id,
		participants:       make(map[string]*Client),
		participantsOrder:  make([]string, 0),
		phase:              "voting",
		story:              "",
		facilitatorTimerCh: make(chan struct{}, 1),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		inbound:            make(chan inboundMessage, 16),
	}
	// attempt to load persisted history for this room
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
	// keep only last 25 if file larger
	if len(h) > 25 {
		h = h[len(h)-25:]
	}
	r.history = h
	return nil
}

func (r *Room) persistHistory() error {
	// ensure dir exists
	path := r.historyFilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// marshal and write
	data, err := json.MarshalIndent(r.history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (r *Room) run() {
	for {
		select {
		case <-r.facilitatorTimerCh:
			// facilitator timeout fired: if still no facilitator, promote first in order
			if r.facilitatorID == "" {
				if len(r.participantsOrder) > 0 {
					r.facilitatorID = r.participantsOrder[0]
				}
			}
			r.lastFacilitator = ""
			r.facilitatorTimer = nil
			r.broadcastStateToAll()

		case c := <-r.register:
			log.Printf("room %s: register client=%s name=%s (before) count=%d", r.ID, c.id, c.name, len(r.participants))
			// if an existing client with same id exists, close its connection (treat as reconnect)
			if existing, ok := r.participants[c.id]; ok && existing != c {
				existing.conn.Close()
			}
			r.participants[c.id] = c
			// maintain join order (avoid duplicates on reconnect)
			found := false
			for _, id := range r.participantsOrder {
				if id == c.id {
					found = true
					break
				}
			}
			if !found {
				r.participantsOrder = append(r.participantsOrder, c.id)
			}
			// if this client was the last facilitator and a timer is pending, restore facilitator
			if r.lastFacilitator == c.id && r.facilitatorTimer != nil {
				if r.facilitatorTimer.Stop() {
					// stopped before firing
				}
				r.facilitatorTimer = nil
				r.facilitatorID = c.id
				r.lastFacilitator = ""
			} else if r.facilitatorID == "" {
				r.facilitatorID = c.id
			}
			r.broadcastStateToAll()
			log.Printf("room %s: registered client=%s name=%s (after) count=%d", r.ID, c.id, c.name, len(r.participants))

		case c := <-r.unregister:
			// ignore stale unregisters (happens when an old connection is closed after a reconnect)
			if cur, ok := r.participants[c.id]; ok && cur != c {
				// stale unregister; do nothing
				continue
			}
			log.Printf("room %s: unregister client=%s name=%s (before) count=%d", r.ID, c.id, c.name, len(r.participants))
			delete(r.participants, c.id)
			// remove from order slice
			for i, id := range r.participantsOrder {
				if id == c.id {
					r.participantsOrder = append(r.participantsOrder[:i], r.participantsOrder[i+1:]...)
					break
				}
			}
			close(c.send)
			if r.facilitatorID == c.id {
				// mark last facilitator and start a short timer before promoting another
				r.lastFacilitator = c.id
				r.facilitatorID = ""
				if r.facilitatorTimer != nil {
					r.facilitatorTimer.Stop()
				}
				// schedule a promotion after a short grace period
				r.facilitatorTimer = time.AfterFunc(5*time.Second, func() {
					// notify room.run via channel to safely mutate room state
					select {
					case r.facilitatorTimerCh <- struct{}{}:
					default:
					}
				})
			}
			if len(r.participants) == 0 {
				// remove room from hub and exit
				if r.facilitatorTimer != nil {
					r.facilitatorTimer.Stop()
				}
				GlobalHub.Delete(r.ID)
				return
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
			return
		}
		// update vote for participant
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
			return
		}
		// archive current votes
		votes := make(map[string]string)
		for _, p := range r.participants {
			if p.participant != nil {
				votes[p.participant.Name] = p.participant.Vote
				p.participant.Vote = ""
			}
		}
		r.history = append(r.history, models.RoundResult{Story: r.story, Votes: votes, Timestamp: time.Now().UTC()})
		if len(r.history) > 25 {
			start := len(r.history) - 25
			r.history = append([]models.RoundResult(nil), r.history[start:]...)
		}
		// persist history to disk
		if err := r.persistHistory(); err != nil {
			log.Printf("room %s: persistHistory error: %v", r.ID, err)
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
	// Build a deterministically-ordered slice of clients (by participant name, then ID)
	clients := make([]*Client, 0, len(r.participants))
	for _, c := range r.participants {
		clients = append(clients, c)
	}
	sort.Slice(clients, func(i, j int) bool {
		a := clients[i].participant
		b := clients[j].participant
		if a.Name == b.Name {
			return a.ID < b.ID
		}
		return a.Name < b.Name
	})

	for _, recipient := range r.participants {
		// build participants slice in deterministic order
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
