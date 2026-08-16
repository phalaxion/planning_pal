package hub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/phalaxion/planning_pal/internal/models"
)

// awaitQueue reads queue updates until one satisfies pred.
//
// Every client gets a queue_update on join and another on every change, so a
// test that just took "the next one" would usually get the empty join payload.
func awaitQueue(t *testing.T, c *Client, pred func([]models.QueueItem) bool) []models.QueueItem {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				t.Fatalf("client %s: send channel closed while awaiting queue", c.id)
			}

			var m models.Message
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("client %s: unmarshal message: %v", c.id, err)
			}
			if m.Type != "queue_update" {
				continue
			}

			var payload struct {
				Queue []models.QueueItem `json:"queue"`
			}
			if err := json.Unmarshal(m.Payload, &payload); err != nil {
				t.Fatalf("client %s: unmarshal queue payload: %v", c.id, err)
			}

			if pred(payload.Queue) {
				return payload.Queue
			}
		case <-deadline:
			t.Fatalf("client %s: timed out awaiting expected queue_update", c.id)
		}
	}
}

func queueOfSize(n int) func([]models.QueueItem) bool {
	return func(q []models.QueueItem) bool { return len(q) == n }
}

func titlesOf(items []models.QueueItem) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.Title)
	}
	return out
}

// ── Capture ─────────────────────────────────────────────────────────────────

func TestAnyoneCanAddToTheQueue(t *testing.T) {
	r, fs := newTestRoom(t)

	join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	// Bob is not the facilitator. Capture is open to everyone.
	send(r, bob, "queue_add", map[string]string{"title": "PP-1 Rework invoices", "notes": "See ENG-1234"})

	queue := awaitQueue(t, bob, queueOfSize(1))

	if queue[0].Title != "PP-1 Rework invoices" {
		t.Errorf("title = %q, want %q", queue[0].Title, "PP-1 Rework invoices")
	}
	if queue[0].Notes != "See ENG-1234" {
		t.Errorf("notes = %q, want %q", queue[0].Notes, "See ENG-1234")
	}
	if queue[0].Status != models.QueuePending {
		t.Errorf("status = %q, want %q", queue[0].Status, models.QueuePending)
	}
	if len(fs.queue) != 1 {
		t.Errorf("persisted %d items, want 1", len(fs.queue))
	}
}

func TestQueueEditingAndRemovalAreFacilitatorOnly(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")
	send(r, alice, "queue_add", map[string]string{"title": "PP-1", "notes": ""})
	queue := awaitQueue(t, bob, queueOfSize(1))
	id := queue[0].ID

	send(r, bob, "queue_edit", map[string]string{"id": id, "title": "hijacked", "notes": ""})
	if code := awaitError(t, bob); code != "not_facilitator" {
		t.Errorf("edit: error code = %q, want %q", code, "not_facilitator")
	}

	send(r, bob, "queue_remove", map[string]string{"id": id})
	if code := awaitError(t, bob); code != "not_facilitator" {
		t.Errorf("remove: error code = %q, want %q", code, "not_facilitator")
	}

	// The facilitator can do both.
	send(r, alice, "queue_edit", map[string]string{"id": id, "title": "PP-1 renamed", "notes": "context"})
	queue = awaitQueue(t, alice, func(q []models.QueueItem) bool {
		return len(q) == 1 && q[0].Title == "PP-1 renamed"
	})

	if queue[0].Notes != "context" {
		t.Errorf("after edit: %+v", queue[0])
	}

	send(r, alice, "queue_remove", map[string]string{"id": id})
	awaitQueue(t, alice, queueOfSize(0))
}

func TestQueueItemTextIsValidated(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	cases := []struct {
		name  string
		title string
		notes string
	}{
		{"empty title", "   ", "notes"},
		{"title too long", strings.Repeat("x", maxTitleLength+1), ""},
		{"notes too long", "PP-1", strings.Repeat("x", maxNotesLength+1)},
	}

	for _, c := range cases {
		send(r, alice, "queue_add", map[string]string{"title": c.title, "notes": c.notes})

		if code := awaitError(t, alice); code != "invalid_queue_item" {
			t.Errorf("%s: error code = %q, want %q", c.name, code, "invalid_queue_item")
		}
	}

	if len(fs.queue) != 0 {
		t.Errorf("persisted %d items, want 0 — rejected input was stored", len(fs.queue))
	}
}

func TestQueueItemTextIsTrimmed(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	send(r, alice, "queue_add", map[string]string{"title": "  PP-1  ", "notes": "  detail  "})
	queue := awaitQueue(t, alice, queueOfSize(1))

	if queue[0].Title != "PP-1" || queue[0].Notes != "detail" {
		t.Errorf("got title=%q notes=%q, want trimmed", queue[0].Title, queue[0].Notes)
	}
}

// ── Starting an item ────────────────────────────────────────────────────────

func TestStartingAQueueItemSetsTheStoryAndItsNotes(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	send(r, alice, "queue_add", map[string]string{"title": "PP-1 Invoices", "notes": "Watch the rounding"})
	queue := awaitQueue(t, alice, queueOfSize(1))

	send(r, alice, "new_round", map[string]string{"itemId": queue[0].ID})
	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1 Invoices" })

	if state.StoryNotes != "Watch the rounding" {
		t.Errorf("storyNotes = %q, want %q", state.StoryNotes, "Watch the rounding")
	}

	// It is being voted on, so it is no longer up next.
	awaitQueue(t, alice, queueOfSize(0))
}

func TestATypedStoryClearsNotesAndRetiresNothing(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	send(r, alice, "queue_add", map[string]string{"title": "PP-1", "notes": "detail"})
	queue := awaitQueue(t, alice, queueOfSize(1))

	send(r, alice, "new_round", map[string]string{"itemId": queue[0].ID})
	awaitState(t, alice, func(s roomState) bool { return s.StoryNotes == "detail" })

	// Type an ad-hoc story instead of taking the next item.
	send(r, alice, "set_story", map[string]string{"story": "Something urgent"})
	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "Something urgent" })

	if state.StoryNotes != "" {
		t.Errorf("storyNotes = %q, want empty for a typed story", state.StoryNotes)
	}

	// Closing this round must not retire the item that is no longer active.
	send(r, alice, "vote", map[string]string{"card": "5"})
	awaitState(t, alice, func(s roomState) bool { return s.participant("a").Voted })

	send(r, alice, "new_round", map[string]string{"story": "next"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "next" })

	if item := fs.queueItem("PP-1"); item == nil || item.Status != models.QueuePending {
		t.Errorf("item was retired by an unrelated round: %+v", item)
	}
}

func TestStartingAnItemThatIsGoneIsRejected(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	send(r, alice, "new_round", map[string]string{"itemId": "ghost"})

	if code := awaitError(t, alice); code != "unknown_queue_item" {
		t.Errorf("error code = %q, want %q", code, "unknown_queue_item")
	}

	// The round must not have half-advanced.
	send(r, alice, "set_story", map[string]string{"story": "PP-2"})
	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if state.Phase != "voting" {
		t.Errorf("phase = %q, want %q", state.Phase, "voting")
	}
}

// ── Retiring ────────────────────────────────────────────────────────────────

func TestAnItemIsRetiredOnlyWhenItsRoundIsRecorded(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	send(r, alice, "queue_add", map[string]string{"title": "PP-1", "notes": ""})
	send(r, alice, "queue_add", map[string]string{"title": "PP-2", "notes": ""})
	queue := awaitQueue(t, alice, queueOfSize(2))

	// Start PP-1, then move on without anyone voting.
	send(r, alice, "new_round", map[string]string{"itemId": queue[0].ID})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	send(r, alice, "new_round", map[string]string{"itemId": queue[1].ID})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if item := fs.queueItem("PP-1"); item == nil || item.Status != models.QueuePending {
		t.Errorf("PP-1 status = %+v, want still pending — nobody voted on it", item)
	}

	// Now vote and close, which does record a round.
	send(r, alice, "vote", map[string]string{"card": "8"})
	awaitState(t, alice, func(s roomState) bool { return s.participant("a").Voted })

	send(r, alice, "new_round", map[string]string{"story": ""})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "" })

	if item := fs.queueItem("PP-2"); item == nil || item.Status != models.QueueDone {
		t.Errorf("PP-2 status = %+v, want done", item)
	}
}

func TestOnlyPendingItemsLoadIntoANewRoom(t *testing.T) {
	fs := &fakeStore{queue: []models.QueueItem{
		{ID: "1", Title: "Yesterday", Status: models.QueueDone},
		{ID: "2", Title: "Still outstanding", Status: models.QueuePending},
	}}

	h := NewHub(fs)
	h.deck = defaultDeck

	r := newRoom(h, t.Name())
	go r.run()

	alice, _ := join(t, r, "a", "Alice")
	queue := awaitQueue(t, alice, queueOfSize(1))

	if queue[0].Title != "Still outstanding" {
		t.Errorf("loaded %v, want only the pending item", titlesOf(queue))
	}
}
