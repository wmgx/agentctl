package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session // key = Session.ID
	filePath string
}

func NewStore(dataDir string) (*Store, error) {
	fp := filepath.Join(dataDir, "sessions.json")
	s := &Store{
		sessions: make(map[string]*Session),
		filePath: fp,
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var sessions []*Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return fmt.Errorf("parse sessions: %w", err)
	}
	for _, sess := range sessions {
		s.sessions[sess.ID] = sess
	}
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0755); err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

func (s *Store) Put(sess *Session) {
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
}

func (s *Store) GetByID(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *Store) GetByChatID(chatID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sess := range s.sessions {
		if sess.ChatID == chatID {
			return sess
		}
	}
	return nil
}

func (s *Store) ListActive() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Session
	for _, sess := range s.sessions {
		if sess.Status == StatusActive || sess.Status == StatusSuspended {
			result = append(result, sess)
		}
	}
	return result
}
