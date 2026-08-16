package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/phalaxion/planning_pal/internal/models"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()

	s, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.DB.Close() })

	return s
}

func saveRound(t *testing.T, s *SQLiteStore, room, id, story string, ts time.Time, votes map[string]string) {
	t.Helper()

	err := s.Save(room, models.RoundResult{
		ID:        id,
		Story:     story,
		Timestamp: ts,
		Votes:     votes,
	})
	if err != nil {
		t.Fatalf("Save(%s): %v", id, err)
	}
}

func storiesOf(rounds []models.RoundResult) []string {
	out := make([]string, 0, len(rounds))
	for _, r := range rounds {
		out = append(out, r.Story)
	}
	return out
}

func TestListReturnsTheMostRecentRoundsInChronologicalOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()

	for i := 1; i <= 15; i++ {
		saveRound(t, s, "ROOM", fmt.Sprintf("r%02d", i), fmt.Sprintf("PP-%d", i),
			base.Add(time.Duration(i)*time.Minute), map[string]string{"Alice": "5"})
	}

	got, err := s.List("ROOM", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 10 {
		t.Fatalf("returned %d rounds, want 10", len(got))
	}
	if got[0].Story != "PP-6" {
		t.Errorf("oldest returned = %q, want %q", got[0].Story, "PP-6")
	}
	if got[9].Story != "PP-15" {
		t.Errorf("newest returned = %q, want %q", got[9].Story, "PP-15")
	}

	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.Before(got[i-1].Timestamp) {
			t.Fatalf("rounds are not chronological: %v", storiesOf(got))
		}
	}
}

// Timestamps are persisted at second resolution, so rounds closed in quick
// succession collide. Without a tiebreaker "the most recent ten" would be
// whichever ten SQLite happened to return.
func TestListBreaksTimestampTiesByInsertionOrder(t *testing.T) {
	s := newTestStore(t)
	ts := time.Unix(1_700_000_000, 0).UTC()

	for i := 1; i <= 5; i++ {
		saveRound(t, s, "ROOM", fmt.Sprintf("r%d", i), fmt.Sprintf("PP-%d", i), ts, nil)
	}

	got, err := s.List("ROOM", 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []string{"PP-3", "PP-4", "PP-5"}
	if fmt.Sprint(storiesOf(got)) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", storiesOf(got), want)
	}
}

func TestListWithoutALimitReturnsEveryRound(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()

	for i := 1; i <= 12; i++ {
		saveRound(t, s, "ROOM", fmt.Sprintf("r%02d", i), fmt.Sprintf("PP-%d", i),
			base.Add(time.Duration(i)*time.Minute), nil)
	}

	got, err := s.List("ROOM", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 12 {
		t.Errorf("returned %d rounds, want 12 — trimming is the room's job, not the store's", len(got))
	}
}

func TestListIsScopedToOneRoom(t *testing.T) {
	s := newTestStore(t)
	ts := time.Unix(1_700_000_000, 0).UTC()

	saveRound(t, s, "ROOM-A", "a1", "A story", ts, nil)
	saveRound(t, s, "ROOM-B", "b1", "B story", ts, nil)

	got, err := s.List("ROOM-A", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 1 || got[0].Story != "A story" {
		t.Errorf("got %v, want just [A story]", storiesOf(got))
	}
}

// The deck contains sentinels that are not numbers. They must survive a round
// trip through a column declared REAL — which they only do because SQLite's
// type affinity is permissive.
func TestNonNumericVotesSurviveARoundTrip(t *testing.T) {
	s := newTestStore(t)

	votes := map[string]string{"Alice": "5", "Bob": "?", "Carol": "☕", "Dave": "999"}
	saveRound(t, s, "ROOM", "r1", "PP-1", time.Unix(1_700_000_000, 0).UTC(), votes)

	got, err := s.List("ROOM", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d rounds, want 1", len(got))
	}

	for name, want := range votes {
		if got[0].Votes[name] != want {
			t.Errorf("vote for %s = %q, want %q", name, got[0].Votes[name], want)
		}
	}
}

// ── Queue ───────────────────────────────────────────────────────────────────

func saveItem(t *testing.T, s *SQLiteStore, room, id, title, notes, status string, ts time.Time) {
	t.Helper()

	err := s.SaveQueueItem(room, models.QueueItem{
		ID: id, Title: title, Notes: notes, Status: status, CreatedAt: ts,
	})
	if err != nil {
		t.Fatalf("SaveQueueItem(%s): %v", id, err)
	}
}

func TestQueueRoundTripsAndKeepsInsertionOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()

	// Same timestamp for two of them, so the rowid tiebreak is exercised.
	saveItem(t, s, "ROOM", "1", "First", "notes one", models.QueuePending, base)
	saveItem(t, s, "ROOM", "2", "Second", "", models.QueuePending, base)
	saveItem(t, s, "ROOM", "3", "Third", "", models.QueuePending, base.Add(time.Minute))

	got, err := s.ListQueue("ROOM", models.QueuePending)
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}

	want := []string{"First", "Second", "Third"}
	names := make([]string, 0, len(got))
	for _, i := range got {
		names = append(names, i.Title)
	}

	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", names, want)
	}
	if got[0].Notes != "notes one" {
		t.Errorf("notes = %q, want %q", got[0].Notes, "notes one")
	}
}

func TestQueueFiltersByStatus(t *testing.T) {
	s := newTestStore(t)
	ts := time.Unix(1_700_000_000, 0).UTC()

	saveItem(t, s, "ROOM", "1", "Done yesterday", "", models.QueueDone, ts)
	saveItem(t, s, "ROOM", "2", "Still to do", "", models.QueuePending, ts)

	pending, err := s.ListQueue("ROOM", models.QueuePending)
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}
	if len(pending) != 1 || pending[0].Title != "Still to do" {
		t.Errorf("pending = %+v, want just the outstanding item", pending)
	}

	all, err := s.ListQueue("ROOM", "")
	if err != nil {
		t.Fatalf("ListQueue(all): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("all = %d items, want 2 — done items stay as a record", len(all))
	}
}

func TestQueueIsScopedToOneRoom(t *testing.T) {
	s := newTestStore(t)
	ts := time.Unix(1_700_000_000, 0).UTC()

	saveItem(t, s, "ROOM-A", "a1", "A item", "", models.QueuePending, ts)
	saveItem(t, s, "ROOM-B", "b1", "B item", "", models.QueuePending, ts)

	// An id from another room must not be reachable.
	if err := s.DeleteQueueItem("ROOM-A", "b1"); err != nil {
		t.Fatalf("DeleteQueueItem: %v", err)
	}

	b, err := s.ListQueue("ROOM-B", "")
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}
	if len(b) != 1 {
		t.Errorf("ROOM-B has %d items, want 1 — a cross-room delete got through", len(b))
	}
}

func TestQueueItemUpdateAndDelete(t *testing.T) {
	s := newTestStore(t)
	ts := time.Unix(1_700_000_000, 0).UTC()

	saveItem(t, s, "ROOM", "1", "Original", "old", models.QueuePending, ts)

	err := s.UpdateQueueItem("ROOM", models.QueueItem{
		ID: "1", Title: "Renamed", Notes: "new", Status: models.QueueDone,
	})
	if err != nil {
		t.Fatalf("UpdateQueueItem: %v", err)
	}

	all, _ := s.ListQueue("ROOM", "")
	if len(all) != 1 || all[0].Title != "Renamed" || all[0].Notes != "new" || all[0].Status != models.QueueDone {
		t.Fatalf("after update: %+v", all)
	}
	// The creation time is not part of an update.
	if !all[0].CreatedAt.Equal(ts) {
		t.Errorf("createdAt = %v, want %v", all[0].CreatedAt, ts)
	}

	if err := s.DeleteQueueItem("ROOM", "1"); err != nil {
		t.Fatalf("DeleteQueueItem: %v", err)
	}
	if all, _ := s.ListQueue("ROOM", ""); len(all) != 0 {
		t.Errorf("after delete: %+v", all)
	}
}
