package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/litellm"

	"github.com/andrew/localagent/internal/agent"
)

// StoredSession is the on-disk JSON form of a finished session. Messages is
// the LLM conversation state needed to support /continue — events are for the
// UI, messages are for the model.
type StoredSession struct {
	ID              string            `json:"id"`
	Goal            string            `json:"goal"`
	Model           string            `json:"model"`
	CompactionModel string            `json:"compaction_model,omitempty"`
	ContextTokens   int               `json:"context_tokens,omitempty"`
	Workdir         string            `json:"workdir"`
	ActiveSkills    []string          `json:"active_skills,omitempty"`
	Todos           []agent.Todo      `json:"todos,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         time.Time         `json:"ended_at,omitempty"`
	Status          string            `json:"status"`
	Events          []agent.Event     `json:"events"`
	Messages        []litellm.Message `json:"messages,omitempty"`
}

// FileStore persists sessions as one JSON file per session inside dir. Writes
// are atomic (write to tmp + rename) so a crash mid-write never leaves a
// half-written file.
type FileStore struct {
	mu  sync.Mutex
	dir string
}

func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, errors.New("FileStore: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("FileStore: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

// validIDRe blocks path traversal via the {id} URL parameter when it's used
// to construct a filename. All IDs we generate are hex, so this is strict.
var validIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (s *FileStore) path(id string) (string, error) {
	if !validIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid session id")
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func (s *FileStore) Save(sess StoredSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.path(sess.ID)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sess); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func (s *FileStore) Get(id string) (*StoredSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	return s.loadFile(p)
}

func (s *FileStore) loadFile(path string) (*StoredSession, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var sess StoredSession
	if err := json.NewDecoder(f).Decode(&sess); err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return &sess, nil
}

// List returns all sessions on disk, sorted newest-first by StartedAt.
func (s *FileStore) List() ([]StoredSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]StoredSession, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		sess, err := s.loadFile(filepath.Join(s.dir, name))
		if err != nil {
			// Skip corrupt files rather than fail the whole listing.
			continue
		}
		out = append(out, *sess)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (s *FileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
