package store

import (
	"database/sql"
	"fmt"
	"os"
	"slices"
	"time"

	_ "modernc.org/sqlite"

	"github.com/phalaxion/planning_pal/internal/models"
)

type SQLiteStore struct {
	DB *sql.DB
}

type Migration struct {
	ID      string
	Version int
	Up      string
}

func NewSQLiteStore(filePath string) (*SQLiteStore, error) {
	// A fresh install has no store directory, and SQLite will not create one.
	if err := os.MkdirAll(filePath, 0o755); err != nil {
		return nil, fmt.Errorf("creating store directory %q: %w", filePath, err)
	}

	filePath = filePath + "/sqlite.db"
	db, err := sql.Open("sqlite", filePath)
	if err != nil {
		return nil, err
	}

	// Ensure the migrations table exists and apply provided migrations (if any).
	s := &SQLiteStore{DB: db}

	if err := s.applyMigrations(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

func (s *SQLiteStore) applyMigrations() error {
	var currentVersion int
	err := s.DB.QueryRow("PRAGMA user_version").Scan(&currentVersion)
	if err != nil {
		return err
	}

	migrations := []Migration{}

	if currentVersion < 1 {
		migrations = append(migrations, Migration{
			ID:      "0001_create_rooms_table",
			Version: 1,
			Up: `CREATE TABLE IF NOT EXISTS rooms (
				id TEXT NOT NULL PRIMARY KEY,
				lastupdate INTEGER NOT NULL
			);`,
		})

		migrations = append(migrations, Migration{
			ID:      "0001_create_rounds_table",
			Version: 1,
			Up: `CREATE TABLE IF NOT EXISTS rounds (
				id TEXT NOT NULL PRIMARY KEY,
				roomid TEXT NOT NULL,
				story TEXT NOT NULL,
				averagevote REAL NOT NULL,
				timestamp INTEGER NOT NULL
			);`,
		})

		migrations = append(migrations, Migration{
			ID:      "0001_create_votes_table",
			Version: 1,
			Up: `CREATE TABLE IF NOT EXISTS votes (
				id INTEGER NOT NULL PRIMARY KEY,
				roundid TEXT NOT NULL,
				name TEXT NOT NULL,
				vote REAL NOT NULL
			);`,
		})
	}

	if currentVersion < 2 {
		// votes.vote was declared REAL but has always held card faces, including
		// '?' and '☕'. SQLite's loose affinity stored those as text anyway, so
		// nothing was ever lost — but the declared type is a lie, and a stricter
		// engine would reject it outright. SQLite cannot alter a column type, so
		// the table is rebuilt.
		migrations = append(migrations, Migration{
			ID:      "0002_votes_vote_to_text_create",
			Version: 2,
			Up: `CREATE TABLE votes_new (
				id INTEGER NOT NULL PRIMARY KEY,
				roundid TEXT NOT NULL,
				name TEXT NOT NULL,
				vote TEXT NOT NULL
			);`,
		})

		// Numeric card faces were coerced to REAL on the way in, so '5' is
		// sitting there as 5.0. Casting that straight to TEXT would migrate it
		// to "5.0" and quietly corrupt every numeric vote ever recorded, so
		// integral values go via INTEGER.
		migrations = append(migrations, Migration{
			ID:      "0002_votes_vote_to_text_copy",
			Version: 2,
			Up: `INSERT INTO votes_new (id, roundid, name, vote)
				SELECT id, roundid, name,
					CASE
						WHEN typeof(vote) IN ('integer', 'real') AND vote = CAST(vote AS INTEGER)
							THEN CAST(CAST(vote AS INTEGER) AS TEXT)
						ELSE CAST(vote AS TEXT)
					END
				FROM votes;`,
		})

		migrations = append(migrations, Migration{
			ID:      "0002_votes_vote_to_text_drop",
			Version: 2,
			Up:      `DROP TABLE votes;`,
		})

		migrations = append(migrations, Migration{
			ID:      "0002_votes_vote_to_text_rename",
			Version: 2,
			Up:      `ALTER TABLE votes_new RENAME TO votes;`,
		})
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}

	versionChanged := false
	for _, m := range migrations {
		if _, err := tx.Exec(m.Up); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s failed: %w", m.ID, err)
		}

		if m.Version > 0 && m.Version > currentVersion {
			currentVersion = m.Version
			versionChanged = true
		}

	}

	if versionChanged {
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", currentVersion)); err != nil {
			tx.Rollback()
			return fmt.Errorf("setting user_version for %d failed: %w", currentVersion, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (s *SQLiteStore) Get(room string, roundId string) (*models.RoundResult, error) {
	row := s.DB.QueryRow(`SELECT id, story, averagevote, timestamp FROM rounds WHERE roomid = ?`, room)

	var roundID string
	var story string
	var averageVote float64
	var timestamp int64

	if err := row.Scan(&roundID, &story, &averageVote, &timestamp); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("Round not found")
		}

		return nil, err
	}

	round := &models.RoundResult{
		ID:          roundID,
		Story:       story,
		AverageVote: averageVote,
		Timestamp:   time.Unix(timestamp, 0).UTC(),
	}

	votes, err := s.getVotes(roundId)
	if err != nil {
		return nil, err
	}

	round.Votes = votes

	return round, nil
}

func (s *SQLiteStore) List(room string, limit int) ([]models.RoundResult, error) {
	// Timestamps are stored at second resolution, so rounds closed in quick
	// succession tie. rowid breaks the tie by insertion order, without which
	// "the most recent ten" is not a well defined set.
	query := `SELECT id, story, averagevote, timestamp FROM rounds WHERE roomid = ? ORDER BY timestamp DESC, rowid DESC`
	args := []any{room}

	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	rounds := []models.RoundResult{}

	for rows.Next() {
		var roundId string
		var story string
		var averageVote float64
		var timestamp int64
		if err := rows.Scan(&roundId, &story, &averageVote, &timestamp); err != nil {
			return nil, err
		}

		round := models.RoundResult{
			ID:          roundId,
			Story:       story,
			AverageVote: averageVote,
			Timestamp:   time.Unix(timestamp, 0).UTC(),
		}

		votes, err := s.getVotes(roundId)
		if err != nil {
			return nil, err
		}

		round.Votes = votes

		rounds = append(rounds, round)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The query selects newest first so LIMIT takes the right end; callers
	// expect chronological order.
	slices.Reverse(rounds)

	return rounds, nil
}

func (s *SQLiteStore) Save(room string, result models.RoundResult) error {
	timestamp := time.Now().Unix()

	roomStatement := `INSERT INTO rooms(id, lastupdate) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET lastupdate = ?`
	_, err := s.DB.Exec(roomStatement, room, timestamp, timestamp)
	if err != nil {
		return err
	}

	roundStatement := `INSERT INTO rounds(id, roomid, story, averagevote, timestamp) VALUES(?, ?, ?, ?, ?)`
	_, err = s.DB.Exec(roundStatement, result.ID, room, result.Story, result.AverageVote, result.Timestamp.Unix())
	if err != nil {
		return err
	}

	voteStatement := `INSERT INTO votes(roundid, name, vote) VALUES(?, ?, ?)`
	for name, vote := range result.Votes {
		_, err = s.DB.Exec(voteStatement, result.ID, name, vote)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *SQLiteStore) Delete(room string, roundId string) error {
	_, err := s.DB.Exec(`DELETE FROM votes WHERE roundid = ?`, roundId)
	if err != nil {
		return err
	}

	_, err = s.DB.Exec(`DELETE FROM rounds WHERE id = ? and roomid = ?`, roundId, room)
	if err != nil {
		return err
	}

	return nil
}

func (s *SQLiteStore) getVotes(roundId string) (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT name, vote FROM votes WHERE roundid = ?`, roundId)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	votes := make(map[string]string)

	for rows.Next() {
		var name string
		var vote string
		if err := rows.Scan(&name, &vote); err != nil {
			return nil, err
		}

		votes[name] = vote
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return votes, nil
}
