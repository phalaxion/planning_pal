package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/guid"
	"github.com/phalaxion/planning_pal/internal/models"
)

type inboundMessage struct {
	client *Client
	msg    models.Message
}

// defaultGracePeriod is how long the room waits before reassigning a departed
// facilitator's role, and before tearing itself down once empty. Both exist to
// let a browser refresh complete without losing state.
const defaultGracePeriod = 15 * time.Second

// historyWindow is how many recent rounds a room keeps in memory and sends to
// clients. The store keeps every round; anything older than the window is only
// reachable through it.
const historyWindow = 10

type Room struct {
	ID              string
	participants    map[string]*Client
	phase           string
	story           string
	facilitatorID   string
	lastFacilitator string
	// Kept so the room can say *who* it is waiting for — the client is gone
	// from participants by then, so their name is no longer reachable.
	lastFacilitatorName string
	facilitatorTimer    *time.Timer
	facilitatorTimerCh  chan struct{}
	cleanupTimer        *time.Timer
	cleanupTimerCh      chan struct{}
	history             []models.RoundResult

	// deck is presentation only — the server never interprets card faces, it
	// just tells clients which ones to offer.
	deck []string

	// queue holds only the pending items. Done ones stay in the store as a
	// record and are never loaded again, so a session starts on a clean list.
	queue []models.QueueItem

	// activeItemID is the queue item the current story came from, if any. It is
	// what gets marked done when a round is actually recorded; a story typed by
	// hand leaves it empty.
	activeItemID string
	storyNotes   string

	// Grace periods, held as fields so tests can shorten them. Only read by
	// run(), and only ever written before run() starts.
	facilitatorGrace time.Duration
	cleanupDelay     time.Duration

	register   chan *Client
	unregister chan *Client
	inbound    chan inboundMessage

	// hub is held so a room can remove itself once empty. It used to reach for
	// a package-level global to do that.
	hub   *Hub
	store Store
}

func newRoom(h *Hub, id string) *Room {
	r := &Room{
		ID:                 id,
		hub:                h,
		deck:               h.deck,
		participants:       make(map[string]*Client),
		phase:              "voting",
		story:              "",
		facilitatorTimerCh: make(chan struct{}, 1),
		cleanupTimerCh:     make(chan struct{}, 1),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		inbound:            make(chan inboundMessage, 16),
		facilitatorGrace:   defaultGracePeriod,
		cleanupDelay:       defaultGracePeriod,

		store: h.store,
	}

	history, err := r.store.List(r.ID, historyWindow)

	if err != nil {
		log.Printf("room %s: failed to load history error: %v", id, err)
	} else {
		r.history = history
	}

	queue, err := r.store.ListQueue(r.ID, models.QueuePending)

	if err != nil {
		log.Printf("room %s: failed to load queue: %v", id, err)
	} else {
		r.queue = queue
	}

	return r
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
			r.lastFacilitatorName = ""
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

			r.hub.Delete(r.ID)

			log.Printf("room %s: closed room", r.ID)

		case c := <-r.register:
			log.Printf("room %s: register client=%s name=%s (before) count=%d", r.ID, c.id, c.name, len(r.participants))

			if r.cleanupTimer != nil {
				log.Printf("room %s: stopped cleanup timer", r.ID)

				r.cleanupTimer.Stop()
				r.cleanupTimer = nil
			}

			for _, existing := range r.participants {
				// Case-insensitive: "Alice" and "alice" are indistinguishable in
				// the participant list, and stored votes are keyed by name, so
				// allowing both would split one person across two history columns.
				if existing.id != c.id && strings.EqualFold(existing.name, c.name) {
					c.handleError("name_taken", fmt.Sprintf("'%s' is already taken in the room. Please choose a different name.", c.name), true)
					break roomAction
				}
			}

			// if an existing client with same id exists, close its connection (treat as reconnect)
			if existing, ok := r.participants[c.id]; ok && existing != c {
				existing.shutdown()
			}

			r.participants[c.id] = c

			if r.lastFacilitator == c.id && r.facilitatorTimer != nil {
				// if this client was the last facilitator and a timer is pending, restore facilitator
				r.facilitatorTimer.Stop()
				r.facilitatorTimer = nil
				r.facilitatorID = c.id
				r.lastFacilitator = ""
				r.lastFacilitatorName = ""
			} else if r.facilitatorID == "" {
				// Otherwsie if we do not have a facilitator, assign to the new client (first-come-first-serve)
				r.facilitatorID = c.id
			}

			r.sendConfig(c)
			r.broadcastStateToAll()
			r.sendHistory(c)
			r.sendQueue(c)

			log.Printf("room %s: registered client=%s name=%s (after) count=%d", r.ID, c.id, c.name, len(r.participants))

		case c := <-r.unregister:
			// If this client was never registered (e.g. rejected for name_taken),
			// or was already dropped for being unresponsive, just shut it down.
			if _, ok := r.participants[c.id]; !ok {
				c.shutdown()
				continue
			}

			// ignore stale unregisters (happens when an old connection is closed after a reconnect)
			if cur, ok := r.participants[c.id]; ok && cur != c {
				// stale unregister; do nothing
				continue
			}

			log.Printf("room %s: unregister client=%s name=%s (before) count=%d", r.ID, c.id, c.name, len(r.participants))

			delete(r.participants, c.id)
			c.shutdown()

			// If this was the facilitator make a note and start a timer to promote another after a grace period (to allow for quick rejoins without losing facilitator role)
			if r.facilitatorID == c.id {
				r.lastFacilitator = c.id
				r.lastFacilitatorName = c.name
				r.facilitatorID = ""
				if r.facilitatorTimer != nil {
					r.facilitatorTimer.Stop()
				}

				log.Printf("room %s: facilitator unregistered, starting reassign counter", r.ID)

				r.facilitatorTimer = time.AfterFunc(r.facilitatorGrace, func() {
					r.signal(r.facilitatorTimerCh)
				})
			}

			// If this was the last participant, schedule a cleanup of the room after a short delay (to allow for quick rejoins without losing state)
			if len(r.participants) == 0 {
				if r.cleanupTimer != nil {
					r.cleanupTimer.Stop()
				}

				log.Printf("room %s: no participants, started cleanup counter", r.ID)

				r.cleanupTimer = time.AfterFunc(r.cleanupDelay, func() {
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

// facilitatorOnly lists the message types that drive the round and are therefore
// restricted to the facilitator. The frontend hides these controls, but the
// socket accepts anything, so the check has to be enforced here to mean anything.
//
// promote is deliberately absent. The role is assigned to whoever connects
// first, which is rarely the person who should hold it — so anyone in the room
// can hand it over from the admin page. That is the recovery path for a scrum
// master who joined second, and gating it behind the role it grants would make
// it useless in exactly the case it exists for.
// queue_add is absent for the same reason as promote: the list is built during
// the day by whoever scoped the work, long before anyone is "the facilitator".
// Editing and removing are restricted, so one person cannot quietly delete
// another's item.
var facilitatorOnly = map[string]bool{
	"reveal":       true,
	"new_round":    true,
	"set_story":    true,
	"queue_edit":   true,
	"queue_remove": true,
}

// Bounds on stored, broadcast text. Rejected rather than truncated: silently
// storing half of what someone typed is worse than telling them.
const (
	maxTitleLength = 200
	maxNotesLength = 2000
)

// validateItemText rejects empty or oversized queue text, returning a message
// suitable for showing to the person who typed it.
func validateItemText(title, notes string) (string, string, error) {
	title = strings.TrimSpace(title)
	notes = strings.TrimSpace(notes)

	switch {
	case title == "":
		return "", "", fmt.Errorf("An item needs a title.")
	case len([]rune(title)) > maxTitleLength:
		return "", "", fmt.Errorf("Titles are limited to %d characters.", maxTitleLength)
	case len([]rune(notes)) > maxNotesLength:
		return "", "", fmt.Errorf("Notes are limited to %d characters.", maxNotesLength)
	}

	return title, notes, nil
}

func (r *Room) handleClientMessage(c *Client, m models.Message) {
	if facilitatorOnly[m.Type] && c.id != r.facilitatorID {
		c.handleError("not_facilitator", "Only the facilitator can do that.", false)
		return
	}

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
			Story  string `json:"story"`
			ItemID string `json:"itemId"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			log.Printf("invalid new_round payload: %v", err)
			c.handleError("invalid_new_round", "Invalid new_round payload provided", false)
			return
		}

		// Resolve the next story before closing the current one, so a bad item
		// id doesn't leave the round half-advanced.
		nextStory, nextNotes := strings.TrimSpace(payload.Story), ""
		nextItemID := ""

		if payload.ItemID != "" {
			idx := r.queueIndex(payload.ItemID)
			if idx < 0 {
				c.handleError("unknown_queue_item", "That item is no longer in the queue.", false)
				return
			}

			nextStory = r.queue[idx].Title
			nextNotes = r.queue[idx].Notes
			nextItemID = r.queue[idx].ID
		}

		votes := make(map[string]string)
		for _, p := range r.participants {
			if p.participant != nil && p.participant.Vote != "" {
				votes[p.participant.Name] = p.participant.Vote
				p.participant.Vote = ""
			}
		}

		// A facilitator skipping past a story shouldn't leave a round behind with
		// nothing in it — it would consume a slot in the capped history window
		// and show as an empty row forever.
		recorded := len(votes) > 0

		if recorded {
			// The average is computed here, not by the client. The client can
			// only average what it can see, and during the voting phase everyone
			// else's vote reads "hidden" — so a round closed without revealing
			// first would record the facilitator's own vote as the average. The
			// room is the only place the real votes exist.
			result := models.RoundResult{
				ID:          guid.New().String(),
				Story:       r.story,
				AverageVote: averageVote(votes),
				Votes:       votes,
				Timestamp:   time.Now().UTC(),
			}

			if err := r.store.Save(r.ID, result); err != nil {
				log.Printf("room %s - failed to save round result: %v", r.ID, err)
				c.handleError("history_failed", fmt.Sprintf("Failed to save round result: %v", err), false)
			}

			// Keep the window bounded regardless of how long a session runs. The
			// tail is copied into a fresh slice rather than re-sliced so the
			// dropped rounds do not stay reachable through the backing array.
			r.history = append(r.history, result)
			if len(r.history) > historyWindow {
				trimmed := make([]models.RoundResult, historyWindow)
				copy(trimmed, r.history[len(r.history)-historyWindow:])
				r.history = trimmed
			}
		}

		// The item is retired only if the round it backed was actually recorded.
		// Pulling something up, talking about it and moving on without voting
		// leaves it pending, so it comes back rather than vanishing silently.
		if recorded && r.activeItemID != "" {
			if idx := r.queueIndex(r.activeItemID); idx >= 0 {
				done := r.queue[idx]
				done.Status = models.QueueDone

				if err := r.store.UpdateQueueItem(r.ID, done); err != nil {
					log.Printf("room %s: failed to mark queue item done: %v", r.ID, err)
				}

				r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
			}
		}

		r.story = nextStory
		r.storyNotes = nextNotes
		r.activeItemID = nextItemID
		r.phase = "voting"
		r.broadcastStateToAll()

		if recorded {
			r.broadcastHistoryToAll()
		}

		// Always: which item is active changed, and the active one is hidden
		// from the list.
		r.broadcastQueueToAll()
	case "queue_add":
		var payload struct {
			Title string `json:"title"`
			Notes string `json:"notes"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			c.handleError("invalid_queue_item", "Invalid queue item provided", false)
			return
		}

		title, notes, err := validateItemText(payload.Title, payload.Notes)
		if err != nil {
			c.handleError("invalid_queue_item", err.Error(), false)
			return
		}

		item := models.QueueItem{
			ID:        guid.New().String(),
			Title:     title,
			Notes:     notes,
			Status:    models.QueuePending,
			CreatedAt: time.Now().UTC(),
		}

		if err := r.store.SaveQueueItem(r.ID, item); err != nil {
			log.Printf("room %s: failed to save queue item: %v", r.ID, err)
			c.handleError("queue_failed", "Could not save that item.", false)
			return
		}

		r.queue = append(r.queue, item)
		r.broadcastQueueToAll()
	case "queue_edit":
		var payload struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Notes string `json:"notes"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			c.handleError("invalid_queue_item", "Invalid queue item provided", false)
			return
		}

		title, notes, err := validateItemText(payload.Title, payload.Notes)
		if err != nil {
			c.handleError("invalid_queue_item", err.Error(), false)
			return
		}

		idx := r.queueIndex(payload.ID)
		if idx < 0 {
			c.handleError("unknown_queue_item", "That item is no longer in the queue.", false)
			return
		}

		item := r.queue[idx]
		item.Title = title
		item.Notes = notes

		if err := r.store.UpdateQueueItem(r.ID, item); err != nil {
			log.Printf("room %s: failed to update queue item: %v", r.ID, err)
			c.handleError("queue_failed", "Could not save that change.", false)
			return
		}

		r.queue[idx] = item
		r.broadcastQueueToAll()
	case "queue_remove":
		var payload struct {
			ID string `json:"id"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return
		}

		idx := r.queueIndex(payload.ID)
		if idx < 0 {
			c.handleError("unknown_queue_item", "That item is no longer in the queue.", false)
			return
		}

		if err := r.store.DeleteQueueItem(r.ID, payload.ID); err != nil {
			log.Printf("room %s: failed to delete queue item: %v", r.ID, err)
			c.handleError("queue_failed", "Could not remove that item.", false)
			return
		}

		r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
		r.broadcastQueueToAll()
	case "set_story":
		var payload struct {
			Story string `json:"story"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return
		}

		// A hand-typed story is no longer backed by a queue item, so it carries
		// no notes and retires nothing when the round closes.
		r.story = payload.Story
		r.storyNotes = ""
		r.activeItemID = ""
		r.broadcastStateToAll()
	case "promote":
		var payload struct {
			ID string `json:"id"`
		}

		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return
		}

		// Promoting someone who isn't here would leave the room with a
		// facilitator nobody can reach: the reassign timer only starts on
		// unregister, so the room would be permanently stuck.
		if _, ok := r.participants[payload.ID]; !ok {
			c.handleError("unknown_participant", "That person is no longer in the room.", false)
			return
		}

		r.facilitatorID = payload.ID
		r.broadcastStateToAll()
	}
}

// averageVote averages the numeric card faces in a round, ignoring the rest.
//
// Parsing rather than matching known faces keeps this working for any deck a
// deployment configures. Note that 999 parses, and is meant to: it is the "too
// large to quote" card, and wrecking the average is how the room notices
// someone played it. Returns 0 when nothing numeric was cast.
func averageVote(votes map[string]string) float64 {
	sum, count := 0.0, 0

	for _, vote := range votes {
		if n, err := strconv.ParseFloat(vote, 64); err == nil {
			sum += n
			count++
		}
	}

	if count == 0 {
		return 0
	}

	return sum / float64(count)
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
			"roomId":              r.ID,
			"phase":               r.phase,
			"story":               r.story,
			"storyNotes":          r.storyNotes,
			"facilitatorId":       r.facilitatorID,
			"participants":        parts,
			"youId":               recipient.id,
			"awaitingFacilitator": r.awaitingFacilitator(),
		}

		b, _ := json.Marshal(models.Message{Type: "state_update", Payload: mustMarshal(payload)})

		if !recipient.deliver(b) {
			r.dropClient(recipient)
		}
	}
}

// awaitingFacilitator names the departed facilitator while their grace period is
// still running, and returns "" otherwise.
//
// The grace period is deliberate — it lets a facilitator refresh without handing
// the role away — but during it the room has no facilitator, so every client
// hides the controls. Naming who we are waiting for turns fifteen seconds of
// apparently broken UI into something the room can read.
func (r *Room) awaitingFacilitator() string {
	if r.facilitatorID == "" && r.facilitatorTimer != nil {
		return r.lastFacilitatorName
	}

	return ""
}

// queueIndex finds a pending item by id, or -1. Items already retired are not
// in the slice, so a stale id from a client that has not caught up reads as
// "not there" rather than matching the wrong thing.
func (r *Room) queueIndex(id string) int {
	for i := range r.queue {
		if r.queue[i].ID == id {
			return i
		}
	}

	return -1
}

// sendQueue delivers the pending queue to one client. Like history, it changes
// far less often than room state, so it travels on its own message rather than
// riding along with every vote.
func (r *Room) sendQueue(c *Client) {
	// The item being voted on is not "up next" — it's now. It stays pending in
	// the store, so if the round is abandoned without a vote it reappears here
	// as soon as something else becomes active.
	queue := make([]models.QueueItem, 0, len(r.queue))
	for _, item := range r.queue {
		if item.ID != r.activeItemID {
			queue = append(queue, item)
		}
	}

	b, _ := json.Marshal(models.Message{
		Type:    "queue_update",
		Payload: mustMarshal(map[string]interface{}{"queue": queue}),
	})

	if !c.deliver(b) {
		r.dropClient(c)
	}
}

func (r *Room) broadcastQueueToAll() {
	for _, c := range r.participants {
		r.sendQueue(c)
	}
}

// sendConfig tells one client which deck to render. Sent before that client's
// first state update, so the cards are known by the time anything draws.
func (r *Room) sendConfig(c *Client) {
	b, _ := json.Marshal(models.Message{
		Type:    "config",
		Payload: mustMarshal(map[string]interface{}{"deck": r.deck}),
	})

	if !c.deliver(b) {
		r.dropClient(c)
	}
}

// sendHistory delivers the room's round history to one client. History only
// changes when a round closes, so it is sent on join and on new_round rather
// than riding along with every state update — which previously meant
// re-serialising every round the room had ever seen on every single vote.
func (r *Room) sendHistory(c *Client) {
	history := r.history
	if history == nil {
		history = []models.RoundResult{}
	}

	b, _ := json.Marshal(models.Message{
		Type:    "history_update",
		Payload: mustMarshal(map[string]interface{}{"history": history}),
	})

	if !c.deliver(b) {
		r.dropClient(c)
	}
}

func (r *Room) broadcastHistoryToAll() {
	for _, c := range r.participants {
		r.sendHistory(c)
	}
}

// dropClient evicts a client the room can no longer deliver to. Removing it
// from participants here is what makes the eventual unregister harmless: the
// room sees a client it no longer knows about and simply shuts it down again,
// rather than tearing the same one down twice.
func (r *Room) dropClient(c *Client) {
	log.Printf("room %s: dropping unresponsive client=%s name=%s", r.ID, c.id, c.name)

	delete(r.participants, c.id)
	c.shutdown()
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
