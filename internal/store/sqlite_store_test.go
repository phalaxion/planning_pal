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
