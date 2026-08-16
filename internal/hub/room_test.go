package hub

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/phalaxion/planning_pal/internal/models"
)

// ── Test doubles ────────────────────────────────────────────────────────────

type fakeStore struct {
	history   []models.RoundResult
	saved     []models.RoundResult
	lastLimit int
}

func (f *fakeStore) Get(room string, id string) (*models.RoundResult, error) { return nil, nil }
func (f *fakeStore) Delete(room string, id string) error                     { return nil }

func (f *fakeStore) List(room string, limit int) ([]models.RoundResult, error) {
	f.lastLimit = limit

	results := f.history
	if limit > 0 && len(results) > limit {
		results = results[len(results)-limit:]
	}

	return results, nil
}

func (f *fakeStore) Save(room string, result models.RoundResult) error {
	f.saved = append(f.saved, result)
	return nil
}

// roomState mirrors the payload built by broadcastStateToAll.
type roomState struct {
	RoomID        string               `json:"roomId"`
	Phase         string               `json:"phase"`
	Story         string               `json:"story"`
	FacilitatorID string               `json:"facilitatorId"`
	Participants  []models.Participant `json:"participants"`
	History       []models.RoundResult `json:"history"`
	YouID         string               `json:"youId"`

	AwaitingFacilitator string `json:"awaitingFacilitator"`
}

func (s roomState) participant(id string) *models.Participant {
	for i := range s.Participants {
		if s.Participants[i].ID == id {
			return &s.Participants[i]
		}
	}
	return nil
}

// ── Harness ─────────────────────────────────────────────────────────────────

// newTestRoom builds a room with grace periods short enough to assert on, and
// starts its run loop. Durations are set before run() starts so the loop never
// races with the write.
func newTestRoom(t *testing.T) (*Room, *fakeStore) {
	t.Helper()

	fs := &fakeStore{}
	var s Store = fs

	r := newRoom(&s, t.Name(), defaultDeck)
	r.facilitatorGrace = 25 * time.Millisecond
	r.cleanupDelay = 25 * time.Millisecond

	go r.run()

	return r, fs
}

// newTestClient builds a client with no websocket connection. Room only ever
// calls close() on a connection, which tolerates nil.
func newTestClient(id, name string) *Client {
	return &Client{
		id:          id,
		name:        name,
		participant: &models.Participant{ID: id, Name: name},
		send:        make(chan []byte, 32),
		done:        make(chan struct{}),
	}
}

// join registers a client and returns the broadcast its registration triggered.
// Registration produces exactly one broadcast, so the state is returned rather
// than discarded — a caller that dropped it would then block forever waiting on
// a second one.
func join(t *testing.T, r *Room, id, name string) (*Client, roomState) {
	t.Helper()

	c := newTestClient(id, name)
	r.register <- c
	state := awaitState(t, c, func(s roomState) bool { return s.participant(id) != nil })

	return c, state
}

// awaitState reads state updates until one satisfies pred. Broadcasts are sent
// to every participant on every change, so tests must skip past the states they
// aren't asserting on rather than assuming the next one matches.
//
// Note that the await* helpers *discard* messages of other types as they scan.
// A test that needs both a config and a state update has to read them in the
// order the room sends them, or register the client by hand.
func awaitState(t *testing.T, c *Client, pred func(roomState) bool) roomState {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				t.Fatalf("client %s: send channel closed while awaiting state", c.id)
			}

			var m models.Message
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("client %s: unmarshal message: %v", c.id, err)
			}
			if m.Type != "state_update" {
				continue
			}

			var s roomState
			if err := json.Unmarshal(m.Payload, &s); err != nil {
				t.Fatalf("client %s: unmarshal state payload: %v", c.id, err)
			}
			if pred(s) {
				return s
			}
		case <-deadline:
			t.Fatalf("client %s: timed out awaiting expected state", c.id)
		}
	}
}

// awaitError reads until an error message arrives and returns its code.
func awaitError(t *testing.T, c *Client) string {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				t.Fatalf("client %s: send channel closed while awaiting error", c.id)
			}

			var m models.Message
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("client %s: unmarshal message: %v", c.id, err)
			}
			if m.Type != "error" {
				continue
			}

			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(m.Payload, &payload); err != nil {
				t.Fatalf("client %s: unmarshal error payload: %v", c.id, err)
			}

			return payload.Code
		case <-deadline:
			t.Fatalf("client %s: timed out awaiting error", c.id)
		}
	}
}

// awaitConfig reads until a config message arrives and returns its deck.
func awaitConfig(t *testing.T, c *Client) []string {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				t.Fatalf("client %s: send channel closed while awaiting config", c.id)
			}

			var m models.Message
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("client %s: unmarshal message: %v", c.id, err)
			}
			if m.Type != "config" {
				continue
			}

			var payload struct {
				Deck []string `json:"deck"`
			}
			if err := json.Unmarshal(m.Payload, &payload); err != nil {
				t.Fatalf("client %s: unmarshal config payload: %v", c.id, err)
			}

			return payload.Deck
		case <-deadline:
			t.Fatalf("client %s: timed out awaiting config", c.id)
		}
	}
}

// awaitHistory reads until a history_update arrives and returns its rounds.
func awaitHistory(t *testing.T, c *Client) []models.RoundResult {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				t.Fatalf("client %s: send channel closed while awaiting history", c.id)
			}

			var m models.Message
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("client %s: unmarshal message: %v", c.id, err)
			}
			if m.Type != "history_update" {
				continue
			}

			var payload struct {
				History []models.RoundResult `json:"history"`
			}
			if err := json.Unmarshal(m.Payload, &payload); err != nil {
				t.Fatalf("client %s: unmarshal history payload: %v", c.id, err)
			}

			return payload.History
		case <-deadline:
			t.Fatalf("client %s: timed out awaiting history_update", c.id)
		}
	}
}

func send(r *Room, c *Client, msgType string, payload interface{}) {
	r.inbound <- inboundMessage{
		client: c,
		msg:    models.Message{Type: msgType, Payload: mustMarshal(payload)},
	}
}

// ── Registration ────────────────────────────────────────────────────────────

func TestFirstClientBecomesFacilitator(t *testing.T) {
	r, _ := newTestRoom(t)

	_, state := join(t, r, "a", "Alice")

	if state.FacilitatorID != "a" {
		t.Errorf("facilitator = %q, want %q", state.FacilitatorID, "a")
	}
	if state.YouID != "a" {
		t.Errorf("youId = %q, want %q", state.YouID, "a")
	}
}

func TestSecondClientDoesNotStealFacilitator(t *testing.T) {
	r, _ := newTestRoom(t)

	join(t, r, "a", "Alice")
	_, state := join(t, r, "b", "Bob")

	if len(state.Participants) != 2 {
		t.Fatalf("participant count = %d, want 2", len(state.Participants))
	}
	if state.FacilitatorID != "a" {
		t.Errorf("facilitator = %q, want %q", state.FacilitatorID, "a")
	}
}

func TestDuplicateNameIsRejected(t *testing.T) {
	r, _ := newTestRoom(t)

	join(t, r, "a", "Alice")

	impostor := newTestClient("b", "Alice")
	r.register <- impostor

	if code := awaitError(t, impostor); code != "name_taken" {
		t.Errorf("error code = %q, want %q", code, "name_taken")
	}

	// The rejected client must not have been added to the room.
	_, state := join(t, r, "c", "Carol")

	if p := state.participant("b"); p != nil {
		t.Errorf("rejected client was added to the room: %+v", p)
	}
	if len(state.Participants) != 2 {
		t.Errorf("participant count = %d, want 2", len(state.Participants))
	}
}

func TestDuplicateNameIsRejectedRegardlessOfCase(t *testing.T) {
	r, _ := newTestRoom(t)

	join(t, r, "a", "Alice")

	impostor := newTestClient("b", "alice")
	r.register <- impostor

	if code := awaitError(t, impostor); code != "name_taken" {
		t.Errorf("error code = %q, want %q", code, "name_taken")
	}

	_, state := join(t, r, "c", "Carol")

	if len(state.Participants) != 2 {
		t.Errorf("participant count = %d, want 2 — a case variant got in", len(state.Participants))
	}
}

// ── Reconnect ───────────────────────────────────────────────────────────────

func TestReconnectWithSameClientIDReplacesConnection(t *testing.T) {
	r, _ := newTestRoom(t)

	join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	// Alice's browser refreshes: same clientId, brand new connection.
	reconnected := newTestClient("a", "Alice")
	r.register <- reconnected

	state := awaitState(t, bob, func(s roomState) bool { return true })

	if len(state.Participants) != 2 {
		t.Errorf("participant count = %d, want 2 (reconnect must not duplicate)", len(state.Participants))
	}
	if state.FacilitatorID != "a" {
		t.Errorf("facilitator = %q, want %q", state.FacilitatorID, "a")
	}
}

func TestStaleUnregisterDoesNotEvictReconnectedClient(t *testing.T) {
	r, _ := newTestRoom(t)

	original, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	reconnected := newTestClient("a", "Alice")
	r.register <- reconnected
	awaitState(t, bob, func(s roomState) bool { return true })

	// The old connection's readPump now notices the socket died and unregisters.
	// This arrives *after* the reconnect and must be ignored.
	r.unregister <- original

	// Force a broadcast we can observe, and confirm Alice survived it. Sent as
	// the reconnected Alice, who still holds the facilitator role.
	send(r, reconnected, "set_story", map[string]string{"story": "PP-1"})
	state := awaitState(t, bob, func(s roomState) bool { return s.Story == "PP-1" })

	if state.participant("a") == nil {
		t.Error("stale unregister evicted the reconnected client")
	}
	if len(state.Participants) != 2 {
		t.Errorf("participant count = %d, want 2", len(state.Participants))
	}
}

// ── Facilitator handoff ─────────────────────────────────────────────────────

func TestFacilitatorReassignedAfterGracePeriod(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	r.unregister <- alice

	state := awaitState(t, bob, func(s roomState) bool { return s.FacilitatorID == "b" })

	if state.FacilitatorID != "b" {
		t.Errorf("facilitator = %q, want %q", state.FacilitatorID, "b")
	}
}

func TestFacilitatorRegainedOnReconnectWithinGracePeriod(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	r.unregister <- alice

	// Alice refreshes and returns before the grace period elapses.
	reconnected := newTestClient("a", "Alice")
	r.register <- reconnected

	state := awaitState(t, bob, func(s roomState) bool { return s.FacilitatorID != "" })

	if state.FacilitatorID != "a" {
		t.Errorf("facilitator = %q, want %q (role should survive a refresh)", state.FacilitatorID, "a")
	}

	// The pending timer must not fire later and hand the role away.
	time.Sleep(3 * r.facilitatorGrace)

	send(r, reconnected, "set_story", map[string]string{"story": "PP-2"})
	state = awaitState(t, bob, func(s roomState) bool { return s.Story == "PP-2" })

	if state.FacilitatorID != "a" {
		t.Errorf("facilitator = %q after grace elapsed, want %q", state.FacilitatorID, "a")
	}
}

// ── Authorisation ───────────────────────────────────────────────────────────
//
// The frontend hides facilitator controls, but the socket accepts anything a
// client cares to send. These assert the server refuses on its own.

func TestNonFacilitatorCannotRevealCards(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, bob, "reveal", nil)

	if code := awaitError(t, bob); code != "not_facilitator" {
		t.Errorf("error code = %q, want %q", code, "not_facilitator")
	}

	// Trigger a legitimate broadcast and confirm the phase never moved.
	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	state := awaitState(t, bob, func(s roomState) bool { return s.Story == "PP-1" })

	if state.Phase != "voting" {
		t.Errorf("phase = %q after a non-facilitator reveal, want %q", state.Phase, "voting")
	}
}

func TestNonFacilitatorCannotSetStory(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, bob, "set_story", map[string]string{"story": "hijacked"})

	if code := awaitError(t, bob); code != "not_facilitator" {
		t.Errorf("error code = %q, want %q", code, "not_facilitator")
	}

	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	state := awaitState(t, bob, func(s roomState) bool { return s.Story != "" })

	if state.Story != "PP-1" {
		t.Errorf("story = %q, want %q", state.Story, "PP-1")
	}
}

// promote is intentionally not facilitator-gated: the role goes to whoever
// connected first, so anyone has to be able to hand it to the person who should
// actually be running the session.
func TestAnyoneCanPromote(t *testing.T) {
	r, _ := newTestRoom(t)

	join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	// Bob joined second and is not the facilitator, but takes the role anyway.
	send(r, bob, "promote", map[string]string{"id": "b"})
	state := awaitState(t, bob, func(s roomState) bool { return s.FacilitatorID == "b" })

	if state.FacilitatorID != "b" {
		t.Errorf("facilitator = %q, want %q", state.FacilitatorID, "b")
	}

	// And the role is real, not cosmetic.
	send(r, bob, "set_story", map[string]string{"story": "PP-1"})
	state = awaitState(t, bob, func(s roomState) bool { return s.Story == "PP-1" })

	if state.Story != "PP-1" {
		t.Errorf("story = %q, want %q", state.Story, "PP-1")
	}
}

func TestPromotingSomeoneWhoIsNotPresentIsStillRejectedForNonFacilitators(t *testing.T) {
	r, _ := newTestRoom(t)

	join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, bob, "promote", map[string]string{"id": "ghost"})

	if code := awaitError(t, bob); code != "unknown_participant" {
		t.Errorf("error code = %q, want %q", code, "unknown_participant")
	}
}

func TestFacilitatorCanPromoteAnotherParticipant(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, alice, "promote", map[string]string{"id": "b"})
	awaitState(t, bob, func(s roomState) bool { return s.FacilitatorID == "b" })

	// Handing the role over must also take it away.
	send(r, alice, "reveal", nil)

	if code := awaitError(t, alice); code != "not_facilitator" {
		t.Errorf("error code = %q, want %q — the old facilitator kept their powers", code, "not_facilitator")
	}
}

func TestPromotingSomeoneWhoIsNotPresentIsRejected(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")

	send(r, alice, "promote", map[string]string{"id": "ghost"})

	if code := awaitError(t, alice); code != "unknown_participant" {
		t.Errorf("error code = %q, want %q", code, "unknown_participant")
	}

	// The room must not be left with a facilitator nobody can reach — the
	// reassign timer only runs on unregister, so that state is unrecoverable.
	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	if state.FacilitatorID != "a" {
		t.Errorf("facilitator = %q, want %q — the room is now stuck", state.FacilitatorID, "a")
	}
}

// During the grace period the room has no facilitator at all, so every client
// hides the controls. These pin the explanation that goes in their place.

func TestRoomNamesTheFacilitatorItIsWaitingFor(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	r.unregister <- alice

	state := awaitState(t, bob, func(s roomState) bool { return s.AwaitingFacilitator != "" })

	if state.AwaitingFacilitator != "Alice" {
		t.Errorf("awaitingFacilitator = %q, want %q", state.AwaitingFacilitator, "Alice")
	}
	if state.FacilitatorID != "" {
		t.Errorf("facilitator = %q, want empty during the grace period", state.FacilitatorID)
	}
}

func TestWaitingNoticeClearsWhenTheRoleIsReassigned(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	r.unregister <- alice

	state := awaitState(t, bob, func(s roomState) bool { return s.FacilitatorID == "b" })

	if state.AwaitingFacilitator != "" {
		t.Errorf("awaitingFacilitator = %q, want empty once the role was reassigned", state.AwaitingFacilitator)
	}
}

func TestWaitingNoticeClearsWhenTheFacilitatorReturns(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	r.unregister <- alice
	awaitState(t, bob, func(s roomState) bool { return s.AwaitingFacilitator == "Alice" })

	reconnected := newTestClient("a", "Alice")
	r.register <- reconnected

	state := awaitState(t, bob, func(s roomState) bool { return s.FacilitatorID == "a" })

	if state.AwaitingFacilitator != "" {
		t.Errorf("awaitingFacilitator = %q, want empty once they came back", state.AwaitingFacilitator)
	}
}

// ── Room lifecycle ──────────────────────────────────────────────────────────

func TestRoomIsRemovedAfterLastParticipantLeaves(t *testing.T) {
	r, _ := newTestRoom(t)

	GlobalHub.mu.Lock()
	GlobalHub.rooms[r.ID] = r
	GlobalHub.mu.Unlock()

	alice, _ := join(t, r, "a", "Alice")
	r.unregister <- alice

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := GlobalHub.Get(r.ID); !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Error("room was not removed from the hub after the last participant left")
}

func TestRejoinCancelsPendingCleanup(t *testing.T) {
	r, _ := newTestRoom(t)

	GlobalHub.mu.Lock()
	GlobalHub.rooms[r.ID] = r
	GlobalHub.mu.Unlock()
	t.Cleanup(func() { GlobalHub.Delete(r.ID) })

	alice, _ := join(t, r, "a", "Alice")
	r.unregister <- alice

	// Rejoin before the cleanup timer fires.
	join(t, r, "a", "Alice")

	time.Sleep(3 * r.cleanupDelay)

	if _, ok := GlobalHub.Get(r.ID); !ok {
		t.Error("room was torn down despite a participant rejoining before cleanup")
	}
}

// ── Voting ──────────────────────────────────────────────────────────────────

func TestVotesAreMaskedDuringVotingAndRevealedOnReveal(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, alice, "vote", map[string]string{"card": "5"})

	// Alice sees her own vote.
	own := awaitState(t, alice, func(s roomState) bool { return s.participant("a").Voted })
	if got := own.participant("a").Vote; got != "5" {
		t.Errorf("alice sees her own vote as %q, want %q", got, "5")
	}

	// Bob sees that she voted, but not what she voted.
	masked := awaitState(t, bob, func(s roomState) bool { return s.participant("a").Voted })
	if got := masked.participant("a").Vote; got != "hidden" {
		t.Errorf("bob sees alice's vote as %q, want %q", got, "hidden")
	}

	// Bob has not voted, so his card stays empty rather than masked.
	if got := masked.participant("b").Vote; got != "" {
		t.Errorf("bob's own unvoted card = %q, want empty", got)
	}
	if masked.participant("b").Voted {
		t.Error("bob is marked as voted before voting")
	}

	send(r, alice, "reveal", nil)

	revealed := awaitState(t, bob, func(s roomState) bool { return s.Phase == "revealed" })
	if got := revealed.participant("a").Vote; got != "5" {
		t.Errorf("after reveal bob sees alice's vote as %q, want %q", got, "5")
	}
}

func TestNewRoundPersistsResultAndClearsVotes(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	send(r, alice, "vote", map[string]string{"card": "5"})
	send(r, bob, "vote", map[string]string{"card": "8"})
	awaitState(t, alice, func(s roomState) bool { return s.participant("b").Voted })

	send(r, alice, "new_round", map[string]interface{}{
		"story": "PP-2",
	})

	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if state.Phase != "voting" {
		t.Errorf("phase = %q, want %q", state.Phase, "voting")
	}
	for _, p := range state.Participants {
		if p.Vote != "" || p.Voted {
			t.Errorf("participant %s carried a vote into the new round: %+v", p.ID, p)
		}
	}

	if len(fs.saved) != 1 {
		t.Fatalf("saved %d round results, want 1", len(fs.saved))
	}

	saved := fs.saved[0]
	if saved.Story != "PP-1" {
		t.Errorf("saved story = %q, want %q (the round being closed, not the next one)", saved.Story, "PP-1")
	}
	if saved.AverageVote != 6.5 {
		t.Errorf("saved average = %v, want 6.5", saved.AverageVote)
	}
	if len(saved.Votes) != 2 {
		t.Errorf("saved %d votes, want 2: %+v", len(saved.Votes), saved.Votes)
	}
}

func TestAverageVote(t *testing.T) {
	cases := []struct {
		name  string
		votes map[string]string
		want  float64
	}{
		{"no votes", map[string]string{}, 0},
		{"single vote", map[string]string{"a": "5"}, 5},
		{"fractional average", map[string]string{"a": "5", "b": "8"}, 6.5},
		{"non-numeric faces ignored", map[string]string{"a": "5", "b": "?", "c": "☕"}, 5},
		{"all non-numeric", map[string]string{"a": "?", "b": "☕"}, 0},
		// 999 is the "too large to quote" card and is meant to wreck the average.
		{"999 is counted", map[string]string{"a": "5", "b": "999"}, 502},
		{"t-shirt deck has no average", map[string]string{"a": "M", "b": "L"}, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := averageVote(c.votes); got != c.want {
				t.Errorf("averageVote(%v) = %v, want %v", c.votes, got, c.want)
			}
		})
	}
}

// The client can only average what it can see, and during voting everyone
// else's vote is masked. Closing a round without revealing first must still
// record the true average.
func TestRoundAverageIsComputedFromRealVotesWithoutRevealing(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, alice, "vote", map[string]string{"card": "5"})
	send(r, bob, "vote", map[string]string{"card": "8"})
	awaitState(t, alice, func(s roomState) bool { return s.participant("b").Voted })

	// Note: no reveal. The phase is still "voting", so alice's own view of bob's
	// vote is "hidden".
	send(r, alice, "new_round", map[string]interface{}{"story": "PP-2"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if len(fs.saved) != 1 {
		t.Fatalf("saved %d rounds, want 1", len(fs.saved))
	}
	if got := fs.saved[0].AverageVote; got != 6.5 {
		t.Errorf("average = %v, want 6.5 (a masked vote was counted, or dropped)", got)
	}
}

func TestRoundAverageIgnoresNonNumericVotes(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	send(r, alice, "vote", map[string]string{"card": "8"})
	send(r, bob, "vote", map[string]string{"card": "☕"})
	awaitState(t, alice, func(s roomState) bool { return s.participant("b").Voted })

	send(r, alice, "new_round", map[string]interface{}{"story": "PP-2"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if got := fs.saved[0].AverageVote; got != 8 {
		t.Errorf("average = %v, want 8", got)
	}
	if len(fs.saved[0].Votes) != 2 {
		t.Errorf("recorded %d votes, want 2 — non-numeric votes still count as cast", len(fs.saved[0].Votes))
	}
}

// Skipping past a story shouldn't burn a slot in the capped history window.
func TestARoundWithNoVotesIsNotRecorded(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	awaitHistory(t, alice)

	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	// Nobody voted. Move on anyway.
	send(r, alice, "new_round", map[string]interface{}{"story": "PP-2"})
	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if len(fs.saved) != 0 {
		t.Errorf("persisted %d rounds, want 0: %+v", len(fs.saved), fs.saved)
	}
	if state.Phase != "voting" {
		t.Errorf("phase = %q, want %q — the round should still advance", state.Phase, "voting")
	}

	// And a real round afterwards is still recorded normally.
	send(r, alice, "vote", map[string]string{"card": "5"})
	awaitState(t, alice, func(s roomState) bool { return s.participant("a").Voted })

	send(r, alice, "new_round", map[string]interface{}{"story": "PP-3"})
	recorded := awaitHistory(t, alice)

	if len(fs.saved) != 1 {
		t.Fatalf("persisted %d rounds, want 1", len(fs.saved))
	}
	if len(recorded) != 1 || recorded[0].Story != "PP-2" {
		t.Errorf("history = %+v, want one round for PP-2", recorded)
	}
}

func TestHistoryIsLoadedFromStoreOnRoomCreation(t *testing.T) {
	fs := &fakeStore{history: []models.RoundResult{
		{ID: "r1", Story: "PP-0", AverageVote: 3, Votes: map[string]string{"Alice": "3"}},
	}}
	var s Store = fs

	r := newRoom(&s, t.Name(), defaultDeck)
	r.facilitatorGrace = 25 * time.Millisecond
	r.cleanupDelay = 25 * time.Millisecond
	go r.run()

	alice, _ := join(t, r, "a", "Alice")
	loaded := awaitHistory(t, alice)

	if len(loaded) != 1 {
		t.Fatalf("history length = %d, want 1", len(loaded))
	}
	if loaded[0].Story != "PP-0" {
		t.Errorf("history[0].story = %q, want %q", loaded[0].Story, "PP-0")
	}
}

func TestStateUpdatesDoNotCarryHistory(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	awaitHistory(t, alice) // the empty history every client gets on join

	send(r, alice, "vote", map[string]string{"card": "5"})
	send(r, alice, "new_round", map[string]interface{}{"story": "PP-2"})

	// The round is now recorded, so history is non-empty...
	recorded := awaitHistory(t, alice)
	if len(recorded) != 1 {
		t.Fatalf("history length = %d, want 1", len(recorded))
	}

	// ...but a plain vote must not drag it along.
	send(r, alice, "vote", map[string]string{"card": "8"})
	state := awaitState(t, alice, func(s roomState) bool { return s.participant("a").Vote == "8" })

	if len(state.History) != 0 {
		t.Errorf("state_update carried %d history entries, want 0", len(state.History))
	}
}

func TestJoiningClientReceivesHistory(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	awaitHistory(t, alice)

	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	send(r, alice, "vote", map[string]string{"card": "5"})
	send(r, alice, "new_round", map[string]interface{}{"story": "PP-2"})
	awaitHistory(t, alice)

	// Bob arrives after the round closed and must still see it.
	bob, _ := join(t, r, "b", "Bob")
	received := awaitHistory(t, bob)

	if len(received) != 1 {
		t.Fatalf("late joiner received %d history entries, want 1", len(received))
	}
	if received[0].Story != "PP-1" {
		t.Errorf("history[0].story = %q, want %q", received[0].Story, "PP-1")
	}
}

func TestHistoryWindowIsCappedDuringALongSession(t *testing.T) {
	r, fs := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	awaitHistory(t, alice)

	// Close more rounds than the window holds, all within one session.
	rounds := historyWindow + 4
	var history []models.RoundResult

	for i := 1; i <= rounds; i++ {
		story := fmt.Sprintf("PP-%d", i)
		send(r, alice, "set_story", map[string]string{"story": story})
		awaitState(t, alice, func(s roomState) bool { return s.Story == story })

		send(r, alice, "vote", map[string]string{"card": "5"})
		send(r, alice, "new_round", map[string]interface{}{"story": ""})
		history = awaitHistory(t, alice)
	}

	if len(history) != historyWindow {
		t.Fatalf("history length = %d, want %d", len(history), historyWindow)
	}

	// The window must hold the newest rounds, in chronological order.
	if got, want := history[0].Story, fmt.Sprintf("PP-%d", rounds-historyWindow+1); got != want {
		t.Errorf("oldest retained story = %q, want %q", got, want)
	}
	if got, want := history[len(history)-1].Story, fmt.Sprintf("PP-%d", rounds); got != want {
		t.Errorf("newest retained story = %q, want %q", got, want)
	}

	// Trimming is a display concern — every round must still be persisted.
	if len(fs.saved) != rounds {
		t.Errorf("persisted %d rounds, want %d — trimming must not drop history from the store", len(fs.saved), rounds)
	}
}

func TestRoomAsksTheStoreOnlyForTheWindow(t *testing.T) {
	fs := &fakeStore{}
	for i := 1; i <= historyWindow+5; i++ {
		fs.history = append(fs.history, models.RoundResult{
			ID:    fmt.Sprintf("r%d", i),
			Story: fmt.Sprintf("PP-%d", i),
		})
	}

	var s Store = fs
	r := newRoom(&s, t.Name(), defaultDeck)
	r.facilitatorGrace = 25 * time.Millisecond
	r.cleanupDelay = 25 * time.Millisecond
	go r.run()

	if fs.lastLimit != historyWindow {
		t.Errorf("store queried with limit %d, want %d", fs.lastLimit, historyWindow)
	}

	alice, _ := join(t, r, "a", "Alice")
	loaded := awaitHistory(t, alice)

	if len(loaded) != historyWindow {
		t.Fatalf("loaded %d rounds, want %d", len(loaded), historyWindow)
	}
	if got, want := loaded[len(loaded)-1].Story, fmt.Sprintf("PP-%d", historyWindow+5); got != want {
		t.Errorf("newest loaded story = %q, want %q", got, want)
	}
}

func TestClosingARoundBroadcastsHistoryToEveryone(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	awaitHistory(t, alice)
	awaitHistory(t, bob)

	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	send(r, bob, "vote", map[string]string{"card": "3"})
	send(r, alice, "new_round", map[string]interface{}{"story": "PP-2"})

	// Bob did not close the round, but must still see it appear.
	received := awaitHistory(t, bob)

	if len(received) != 1 {
		t.Fatalf("bob received %d history entries, want 1", len(received))
	}
	if received[0].Story != "PP-1" {
		t.Errorf("history[0].story = %q, want %q", received[0].Story, "PP-1")
	}
}

func TestSlowClientIsDroppedWithoutPanicking(t *testing.T) {
	r, _ := newTestRoom(t)

	alice, _ := join(t, r, "a", "Alice")
	bob, _ := join(t, r, "b", "Bob")

	// Bob stops reading. Fill his buffer so the next broadcast cannot queue.
	for len(bob.send) < cap(bob.send) {
		bob.send <- []byte("{}")
	}

	// This broadcast finds Bob's buffer full and drops him. Its own payload was
	// built before the drop, so it still lists him.
	send(r, alice, "set_story", map[string]string{"story": "PP-1"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-1" })

	// The next broadcast is the first one built after the drop. Receiving it
	// also proves the room goroutine got past the drop without dying.
	send(r, alice, "set_story", map[string]string{"story": "PP-2"})
	state := awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-2" })

	if state.participant("b") != nil {
		t.Error("unresponsive client was not removed from the room")
	}

	select {
	case <-bob.done:
	default:
		t.Error("dropped client was not shut down")
	}

	// Bob's socket then dies and his readPump unregisters him, as it would in
	// production once writePump sees the shutdown signal and tears the conn
	// down. This second teardown must be harmless.
	r.unregister <- bob

	// The room must still be alive and serving everyone else.
	send(r, alice, "set_story", map[string]string{"story": "PP-3"})
	awaitState(t, alice, func(s roomState) bool { return s.Story == "PP-3" })
}
