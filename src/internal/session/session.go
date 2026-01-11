package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"src/internal/authclient"
)

type Store struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
}

type Session struct {
	ID        string
	User      authclient.User
	CreatedAt time.Time
}

func NewStore(ttl time.Duration) *Store {
	return &Store{
		sessions: make(map[string]Session),
		ttl:      ttl,
	}
}

func (s *Store) Create(u authclient.User) (Session, error) {
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()

	ss := Session{
		ID:        id,
		User:      u,
		CreatedAt: now,
	}

	s.mu.Lock()
	s.sessions[id] = ss
	s.mu.Unlock()

	return ss, nil
}

func (s *Store) Get(id string) (Session, bool) {
	s.mu.RLock()
	ss, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return Session{}, false
	}
	if time.Since(ss.CreatedAt) > s.ttl {
		s.Delete(id)
		return Session{}, false
	}
	return ss, true
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func newID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
