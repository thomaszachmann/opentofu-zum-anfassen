package main

import (
	"context"
	"database/sql"
	"sync"

	"github.com/google/uuid"
)

type note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// store speichert Notizen – im Speicher oder in Postgres.
type store interface {
	Kind() string
	List(ctx context.Context) ([]note, error)
	Add(ctx context.Context, text string) (note, error)
	Ping(ctx context.Context) error
}

type memStore struct {
	mu    sync.Mutex
	notes []note
}

func newMemStore() *memStore { return &memStore{notes: []note{}} }

func (s *memStore) Kind() string                   { return "memory" }
func (s *memStore) Ping(ctx context.Context) error { return nil }

func (s *memStore) List(ctx context.Context) ([]note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]note{}, s.notes...), nil
}

func (s *memStore) Add(ctx context.Context, text string) (note, error) {
	n := note{ID: uuid.NewString(), Text: text}
	s.mu.Lock()
	s.notes = append(s.notes, n)
	s.mu.Unlock()
	return n, nil
}

type pgStore struct {
	db *sql.DB
}

func (s *pgStore) Kind() string                   { return "postgres" }
func (s *pgStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *pgStore) List(ctx context.Context) ([]note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id::text, text FROM notes ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.ID, &n.Text); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

func (s *pgStore) Add(ctx context.Context, text string) (note, error) {
	n := note{ID: uuid.NewString(), Text: text}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notes (id, text) VALUES ($1, $2)`, n.ID, n.Text)
	return n, err
}
