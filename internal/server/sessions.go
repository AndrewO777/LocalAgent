package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/voocel/litellm"

	"github.com/andrew/localagent/internal/agent"
)

// IsValidID reports whether id is a well-formed session identifier. Used by
// handlers to give a 400 on garbage input before any store lookup.
func IsValidID(id string) bool { return validIDRe.MatchString(id) }

// Summary is the lightweight view of a session used in list responses.
type Summary struct {
	ID           string       `json:"id"`
	Goal         string       `json:"goal"`
	Model        string       `json:"model"`
	Workdir      string       `json:"workdir"`
	StartedAt    time.Time    `json:"started_at"`
	EndedAt      time.Time    `json:"ended_at,omitempty"`
	Status       string       `json:"status"`
	EventCount   int          `json:"event_count"`
	ActiveSkills []string     `json:"active_skills,omitempty"`
	Todos        []agent.Todo `json:"todos,omitempty"`
}

// Session holds the per-run state: a buffered history of events plus active
// subscriber channels for live tailing via SSE.
type Session struct {
	ID              string
	Goal            string
	Model           string
	CompactionModel string
	ContextTokens   int
	Workdir         string

	mu               sync.Mutex
	activeSkills     []string // skill names active at the most recent run start
	todos            []agent.Todo // latest plan emitted by update_todos
	startedAt        time.Time
	endedAt          time.Time
	status           string // running | finished | error | canceled | max_iter | unknown
	history          []agent.Event
	messages         []litellm.Message
	subscribers      []chan agent.Event
	done             bool
	cancel           context.CancelFunc
	pendingQuestions map[string]chan string // question_id -> 1-buffered answer channel
	store            *FileStore
}

// Todos returns a defensive copy of the session's latest todo list.
func (s *Session) Todos() []agent.Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agent.Todo(nil), s.todos...)
}

// --- ask_user wiring -------------------------------------------------------

// RegisterQuestion creates a 1-buffered channel under the given question ID
// and returns it. The caller (ask_user tool handler) waits on this channel
// for the user's answer. Caller MUST call UnregisterQuestion when done,
// typically via defer, so canceled / abandoned questions don't leak.
func (s *Session) RegisterQuestion(id string) chan string {
	ch := make(chan string, 1)
	s.mu.Lock()
	if s.pendingQuestions == nil {
		s.pendingQuestions = make(map[string]chan string)
	}
	s.pendingQuestions[id] = ch
	s.mu.Unlock()
	return ch
}

// UnregisterQuestion removes a question from the pending map. Safe to call
// after Answer (no-op).
func (s *Session) UnregisterQuestion(id string) {
	s.mu.Lock()
	delete(s.pendingQuestions, id)
	s.mu.Unlock()
}

// Answer delivers a user-supplied answer to a waiting ask_user handler.
// Returns false if the question_id isn't pending (already answered, canceled,
// or unknown). The send is non-blocking because the channel is 1-buffered
// and no other writer exists; double-answer attempts are ignored.
func (s *Session) Answer(id, answer string) bool {
	s.mu.Lock()
	ch, ok := s.pendingQuestions[id]
	if ok {
		delete(s.pendingQuestions, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- answer:
		return true
	default:
		// Reader already gone (handler canceled). Treat as no-op rather
		// than blocking forever.
		return false
	}
}

// PendingQuestions returns the IDs of questions currently awaiting an
// answer. Useful for the UI when reconnecting via SSE replay — clients
// can decide whether to show the prompt input.
func (s *Session) PendingQuestions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.pendingQuestions))
	for id := range s.pendingQuestions {
		out = append(out, id)
	}
	return out
}

// ActiveSkills returns a defensive copy of the skill names active at the
// most recent run start. Safe to call from any goroutine.
func (s *Session) ActiveSkills() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.activeSkills...)
}

// SetActiveSkills replaces the active-skills list. Called by the run /
// continue handlers right before agent.Run launches.
func (s *Session) SetActiveSkills(names []string) {
	s.mu.Lock()
	s.activeSkills = append([]string(nil), names...)
	s.mu.Unlock()
}

// Messages returns a defensive copy of the current LLM conversation.
func (s *Session) Messages() []litellm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]litellm.Message(nil), s.messages...)
}

// SetMessages replaces the stored conversation. Called by the agent's
// OnMessages callback after each iteration.
func (s *Session) SetMessages(m []litellm.Message) {
	s.mu.Lock()
	s.messages = append([]litellm.Message(nil), m...)
	s.mu.Unlock()
}

// Reopen prepares a finished session for another agent run. Used by the
// continue endpoint. Caller must supply the new cancel func.
func (s *Session) Reopen(newCancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = false
	s.status = "running"
	s.endedAt = time.Time{}
	s.cancel = newCancel
	// A new run starts with no questions pending — defensive in case a
	// prior run leaked a registration somehow.
	s.pendingQuestions = make(map[string]chan string)
}

// SanitizeMessagesForContinue trims a conversation back to its last
// well-formed state. If a previous run was canceled or errored mid-tool-call,
// the trailing assistant turn may have tool_calls with no matching tool
// responses — appending a new user message there would fail OpenAI/Ollama
// schema validation. This walks forward and keeps only the longest prefix
// that ends with all tool_calls answered.
func SanitizeMessagesForContinue(msgs []litellm.Message) []litellm.Message {
	cleanEnd := 0
	pending := 0
	for i, m := range msgs {
		switch m.Role {
		case "assistant":
			pending = len(m.ToolCalls)
		case "tool":
			if pending > 0 {
				pending--
			}
		}
		if pending == 0 {
			cleanEnd = i + 1
		}
	}
	return msgs[:cleanEnd]
}

// Append records an event and broadcasts it to all current subscribers. Slow
// subscribers (full buffer) get the event dropped — they can replay from
// history on reconnect. On a terminal event, the session is persisted to
// disk and subscriber channels are closed.
func (s *Session) Append(e agent.Event) {
	s.mu.Lock()
	s.history = append(s.history, e)
	switch e.Type {
	case agent.EventDone:
		s.done = true
		s.endedAt = time.UnixMilli(e.TimeMS)
		if e.Reason != "" {
			s.status = e.Reason
		} else {
			s.status = "unknown"
		}
	case agent.EventTodoUpdate:
		// Capture the latest plan so /api/sessions/{id} can return it
		// without having to scan history.
		s.todos = append([]agent.Todo(nil), e.Todos...)
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

	if doneFlag {
		s.mu.Lock()
		for _, c := range s.subscribers {
			close(c)
		}
		s.subscribers = nil
		s.mu.Unlock()
		// Persist asynchronously so the agent goroutine doesn't pay disk I/O.
		if s.store != nil {
			stored := s.toStored()
			go func() {
				if err := s.store.Save(stored); err != nil {
					log.Printf("session %s: save failed: %v", s.ID, err)
				}
			}()
		}
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
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Session) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// History returns a copy of every event so far (used to serve the full session JSON).
func (s *Session) History() []agent.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agent.Event(nil), s.history...)
}

func (s *Session) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	if status == "" {
		if s.done {
			status = "unknown"
		} else {
			status = "running"
		}
	}
	return Summary{
		ID:           s.ID,
		Goal:         s.Goal,
		Model:        s.Model,
		Workdir:      s.Workdir,
		StartedAt:    s.startedAt,
		EndedAt:      s.endedAt,
		Status:       status,
		EventCount:   len(s.history),
		ActiveSkills: append([]string(nil), s.activeSkills...),
		Todos:        append([]agent.Todo(nil), s.todos...),
	}
}

func (s *Session) toStored() StoredSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StoredSession{
		ID:              s.ID,
		Goal:            s.Goal,
		Model:           s.Model,
		CompactionModel: s.CompactionModel,
		ContextTokens:   s.ContextTokens,
		Workdir:         s.Workdir,
		ActiveSkills:    append([]string(nil), s.activeSkills...),
		Todos:           append([]agent.Todo(nil), s.todos...),
		StartedAt:       s.startedAt,
		EndedAt:         s.endedAt,
		Status:          s.status,
		Events:          append([]agent.Event(nil), s.history...),
		Messages:        append([]litellm.Message(nil), s.messages...),
	}
}

// sessionFromStored reconstructs a Session from disk. done=true and cancel=nil
// until Reopen() is called, so Subscribe returns history then a closed channel.
func sessionFromStored(ss *StoredSession) *Session {
	return &Session{
		ID:              ss.ID,
		Goal:            ss.Goal,
		Model:           ss.Model,
		CompactionModel: ss.CompactionModel,
		ContextTokens:   ss.ContextTokens,
		Workdir:         ss.Workdir,
		activeSkills:    ss.ActiveSkills,
		todos:           ss.Todos,
		startedAt:       ss.StartedAt,
		endedAt:         ss.EndedAt,
		status:          ss.Status,
		history:         ss.Events,
		messages:        ss.Messages,
		done:            true,
	}
}

// Manager owns the in-memory map of sessions backed by an optional FileStore.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	store    *FileStore
}

func NewManager(store *FileStore) *Manager {
	return &Manager{sessions: make(map[string]*Session), store: store}
}

func (m *Manager) Create(goal, model, compactionModel string, contextTokens int, workdir string, cancel context.CancelFunc) *Session {
	s := &Session{
		ID:              newID(),
		Goal:            goal,
		Model:           model,
		CompactionModel: compactionModel,
		ContextTokens:   contextTokens,
		Workdir:         workdir,
		startedAt:       time.Now(),
		status:          "running",
		cancel:          cancel,
		store:           m.store,
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s
}

// Get returns the session for id. If it isn't in memory, the store is
// consulted; the loaded session is cached so further GETs and the SSE replay
// path share one *Session.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if ok {
		return s, true
	}
	if m.store == nil {
		return nil, false
	}
	stored, err := m.store.Get(id)
	if err != nil {
		return nil, false
	}
	s = sessionFromStored(stored)
	m.mu.Lock()
	if existing, ok := m.sessions[id]; ok {
		m.mu.Unlock()
		return existing, true
	}
	m.sessions[id] = s
	m.mu.Unlock()
	return s, true
}

// Delete cancels the session (if running) and removes it from both memory and
// the file store.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok && !s.Done() {
		s.Cancel()
	}
	if m.store != nil {
		return m.store.Delete(id)
	}
	return nil
}

// Summaries merges in-memory sessions with the on-disk archive, dedup'd by ID.
// Memory wins on conflict because it has the freshest event count.
func (m *Manager) Summaries() []Summary {
	m.mu.Lock()
	inMem := make(map[string]bool, len(m.sessions))
	out := make([]Summary, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Summary())
		inMem[s.ID] = true
	}
	m.mu.Unlock()

	if m.store != nil {
		stored, _ := m.store.List()
		for _, ss := range stored {
			if inMem[ss.ID] {
				continue
			}
			out = append(out, Summary{
				ID:           ss.ID,
				Goal:         ss.Goal,
				Model:        ss.Model,
				Workdir:      ss.Workdir,
				StartedAt:    ss.StartedAt,
				EndedAt:      ss.EndedAt,
				Status:       ss.Status,
				EventCount:   len(ss.Events),
				ActiveSkills: ss.ActiveSkills,
				Todos:        ss.Todos,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
