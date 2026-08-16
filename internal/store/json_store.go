package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sync"

	"github.com/phalaxion/planning_pal/internal/models"
)

type JSONStore struct {
	mu       sync.RWMutex
	FilePath string
}

type resultList struct {
	Results map[string][]models.RoundResult `json:"results"`
}

func NewJSONStore(filePath string) *JSONStore {
	// A fresh install has no store directory. Reads tolerate a missing file, so
	// without this the first write is the first thing to fail — after a round
	// has already been played.
	if err := os.MkdirAll(filePath, 0o755); err != nil {
		log.Printf("creating store directory %q: %v", filePath, err)
	}

	filePath = filePath + "/results.json"
	return &JSONStore{FilePath: filePath}
}

func (s *JSONStore) Get(room string, id string) (*models.RoundResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list, err := s.load()

	if err != nil {
		return nil, err
	}

	for _, result := range list.Results[room] {
		if result.ID == id {
			return &result, nil
		}
	}

	return nil, fmt.Errorf("Result not found")
}

func (s *JSONStore) List(room string, limit int) ([]models.RoundResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list, err := s.load()

	if err != nil {
		return nil, err
	}

	// Results are appended as rounds close, so the tail is the most recent.
	results := list.Results[room]
	if limit > 0 && len(results) > limit {
		results = results[len(results)-limit:]
	}

	return results, nil
}

func (s *JSONStore) Save(room string, result models.RoundResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()

	if err != nil {
		return err
	}

	list.Results[room] = append(list.Results[room], result)

	return s.save(list)
}

func (s *JSONStore) Delete(room string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()

	if err != nil {
		return err
	}

	for i, result := range list.Results[room] {
		if result.ID == id {
			list.Results[room] = slices.Delete(list.Results[room], i, i+1)
			return s.save(list)
		}
	}

	return fmt.Errorf("Result not found")
}

func (s *JSONStore) load() (*resultList, error) {
	file, err := os.Open(s.FilePath)

	if errors.Is(err, os.ErrNotExist) {
		return &resultList{Results: map[string][]models.RoundResult{}}, nil
	}

	if err != nil {
		return nil, err
	}

	defer file.Close()

	var lf resultList
	if err := json.NewDecoder(file).Decode(&lf); err != nil {
		return nil, err
	}

	return &lf, nil
}

func (s *JSONStore) save(lf *resultList) error {
	file, err := os.Create(s.FilePath)

	if err != nil {
		return err
	}

	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(lf)
}
