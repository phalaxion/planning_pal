package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// seedV1Database builds a database at schema version 1 — votes.vote declared
// REAL — and writes votes through it exactly as the old code did.
func seedV1Database(t *testing.T, dir string, votes map[string]string) {
	t.Helper()

	db, err := sql.Open("sqlite", dir+"/sqlite.db")
	if err != nil {
		t.Fatalf("open v1 database: %v", err)
	}
	defer db.Close()

	schema := []string{
		`CREATE TABLE rooms (
			id TEXT NOT NULL PRIMARY KEY,
			lastupdate INTEGER NOT NULL
		);`,
		`CREATE TABLE rounds (
			id TEXT NOT NULL PRIMARY KEY,
			roomid TEXT NOT NULL,
			story TEXT NOT NULL,
			averagevote REAL NOT NULL,
			timestamp INTEGER NOT NULL
		);`,
		`CREATE TABLE votes (
			id INTEGER NOT NULL PRIMARY KEY,
			roundid TEXT NOT NULL,
			name TEXT NOT NULL,
			vote REAL NOT NULL
		);`,
		`INSERT INTO rooms (id, lastupdate) VALUES ('ROOM', 1700000000);`,
		`INSERT INTO rounds (id, roomid, story, averagevote, timestamp)
			VALUES ('r1', 'ROOM', 'PP-1', 5, 1700000000);`,
		`PRAGMA user_version = 1;`,
	}

	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed v1 schema: %v", err)
		}
	}

	for name, vote := range votes {
		_, err := db.Exec(`INSERT INTO votes (roundid, name, vote) VALUES (?, ?, ?)`, "r1", name, vote)
		if err != nil {
			t.Fatalf("seed vote for %s: %v", name, err)
		}
	}
}

func TestMigratingFromV1PreservesVotesExactly(t *testing.T) {
	dir := t.TempDir()

	votes := map[string]string{
		"Alice": "5",
		"Bob":   "?",
		"Carol": "☕",
		"Dave":  "999",
		"Erin":  "13",
	}
	seedV1Database(t, dir, votes)

	// Opening the store runs the migration.
	s, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.DB.Close()

	got, err := s.List("ROOM", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d rounds, want 1", len(got))
	}

	for name, want := range votes {
		// The trap: numeric faces were stored as REAL, so a naive CAST to TEXT
		// turns "5" into "5.0".
		if g := got[0].Votes[name]; g != want {
			t.Errorf("vote for %s = %q, want %q", name, g, want)
		}
	}
}

func TestMigrationLeavesVoteColumnDeclaredText(t *testing.T) {
	dir := t.TempDir()
	seedV1Database(t, dir, map[string]string{"Alice": "5"})

	s, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.DB.Close()

	var declared string
	err = s.DB.QueryRow(`SELECT type FROM pragma_table_info('votes') WHERE name = 'vote'`).Scan(&declared)
	if err != nil {
		t.Fatalf("read column type: %v", err)
	}

	if declared != "TEXT" {
		t.Errorf("votes.vote declared %q, want %q", declared, "TEXT")
	}
}

func TestFreshDatabaseLandsOnTheLatestSchema(t *testing.T) {
	s := newTestStore(t)

	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}

	var declared string
	err := s.DB.QueryRow(`SELECT type FROM pragma_table_info('votes') WHERE name = 'vote'`).Scan(&declared)
	if err != nil {
		t.Fatalf("read column type: %v", err)
	}
	if declared != "TEXT" {
		t.Errorf("votes.vote declared %q, want %q", declared, "TEXT")
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	seedV1Database(t, dir, map[string]string{"Alice": "5", "Bob": "☕"})

	for i := 0; i < 3; i++ {
		s, err := NewSQLiteStore(dir)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}

		got, err := s.List("ROOM", 0)
		if err != nil {
			t.Fatalf("List on open %d: %v", i, err)
		}
		if len(got) != 1 || len(got[0].Votes) != 2 {
			t.Fatalf("open %d: got %d rounds with %d votes, want 1 round with 2 votes",
				i, len(got), len(got[0].Votes))
		}
		if got[0].Votes["Alice"] != "5" || got[0].Votes["Bob"] != "☕" {
			t.Errorf("open %d: votes drifted: %+v", i, got[0].Votes)
		}

		s.DB.Close()
	}
}
