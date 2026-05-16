package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/andrew/localagent/internal/agent"
)

// Session holds the per-run state: a buffered history of events plus active
// subscriber channels for live tailing via SSE.
type Session struct {
	ID     string
	Goal   string
	Model  string
	Workdir string

	mu          sync.Mutex
	history     []agent.Event
	subscribers []chan agent.Event
	done        bool
	cancel      context.CancelFunc
}

// Append records an event and broadcasts it to all current subscribers. Slow
// subscribers (full buffer) get the event dropped — they can replay from
// history on reconnect.
func (s *Session) Append(e agent.Event) {
	s.mu.Lock()
	s.history = append(s.history, e)
	if e.Type == agent.EventDone {
		s.done = true
	}
	subs := append([]chan agent.Event(nil), s.subscribers...)
	doneFlag := s.done
	s.mu.Unlock()

	for _, c := range subs {
		select {
		case c <- e:
		default:
		}
	}
	// Close subscriber channels once the run is over so SSE handlers exit
	// their loops cleanly.
	if doneFlag {
		s.mu.Lock()
		for _, c := range s.subscribers {
			close(c)
		}
		s.subscribers = nil
		s.mu.Unlock()
	}
}

// Subscribe returns a snapshot of the current history plus a channel that will
// receive subsequent events. The unsubscribe func must be called when the
// caller stops reading.
func (s *Session) Subscribe() (history []agent.Event, ch <-chan agent.Event, unsub func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hist := append([]agent.Event(nil), s.history...)
	if s.done {
		// Nothing more will come — return a closed channel.
		c := make(chan agent.Event)
		close(c)
		return hist, c, func() {}
	}
	c := make(chan agent.Event, 64)
	s.subscribers = append(s.subscribers, c)
	unsub = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, x := range s.subscribers {
			if x == c {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				// Drain so the next send doesn't block, then close.
				for {
					select {
					case <-c:
					default:
						close(c)
						return
					}
				}
			}
		}
	}
	return hist, c, unsub
}

func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Session) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// Manager owns the in-memory map of sessions for the lifetime of the server.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

func (m *Manager) Create(goal, model, workdir string, cancel context.CancelFunc) *Session {
	s := &Session{
		ID:      newID(),
		Goal:    goal,
		Model:   model,
		Workdir: workdir,
		cancel:  cancel,
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) List() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
