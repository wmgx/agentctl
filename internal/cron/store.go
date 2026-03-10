package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.RWMutex
	jobs     map[string]*CronJob
	filePath string
}

func NewStore(dataDir string) (*Store, error) {
	fp := filepath.Join(dataDir, "cron_jobs.json")
	s := &Store{
		jobs:     make(map[string]*CronJob),
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
	var jobs []*CronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return fmt.Errorf("parse cron jobs: %w", err)
	}
	for _, j := range jobs {
		s.jobs[j.ID] = j
	}
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	jobs := make([]*CronJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

func (s *Store) Put(job *CronJob) {
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
}

func (s *Store) Get(id string) *CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

func (s *Store) ListEnabled() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*CronJob
	for _, j := range s.jobs {
		if j.Enabled {
			result = append(result, j)
		}
	}
	return result
}

func (s *Store) ListAll() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*CronJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		result = append(result, j)
	}
	return result
}
