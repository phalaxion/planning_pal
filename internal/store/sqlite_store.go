package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

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
	filePath = filePath + "/sqlite.db"
	db, err := sql.Open("sqlite3", filePath)
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

func (s *SQLiteStore) List(room string) ([]models.RoundResult, error) {
	rows, err := s.DB.Query(`SELECT id, story, averagevote, timestamp FROM rounds WHERE roomid = ?`, room)
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
