package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/andrew/localagent/internal/agent"
	"github.com/andrew/localagent/internal/llm"
	"github.com/andrew/localagent/internal/tools"
)

// Server hosts the HTTP API plus the embedded React UI.
type Server struct {
	mgr        *Manager
	static     http.Handler
	defaultHost string
}

// New returns a *Server. staticFS must contain an index.html at its root.
func New(staticFS fs.FS, defaultOllamaHost string) *Server {
	return &Server{
		mgr:         NewManager(),
		static:      http.FileServer(http.FS(staticFS)),
		defaultHost: defaultOllamaHost,
	}
}

// Routes returns an http.Handler with all API endpoints + UI mounted.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/run", s.handleRun)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.handleEvents)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	mux.Handle("/", s.static)
	return withCORS(withLogging(mux))
}

// ---- run ------------------------------------------------------------------

type runRequest struct {
	Model         string `json:"model"`
	Host          string `json:"host"`
	Workdir       string `json:"workdir"`
	Goal          string `json:"goal"`
	MaxIterations int    `json:"max_iterations"`
}

type runResponse struct {
	SessionID string `json:"session_id"`
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	req.Workdir = strings.TrimSpace(req.Workdir)
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Model == "" || req.Workdir == "" || req.Goal == "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("model, workdir and goal are required"))
		return
	}
	if req.MaxIterations <= 0 {
		req.MaxIterations = 25
	}
	host := req.Host
	if host == "" {
		host = s.defaultHost
	}

	reg, err := tools.Build(req.Workdir)
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("workdir: %w", err))
		return
	}
	client, err := llm.New(req.Model, host)
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("llm: %w", err))
		return
	}

	// The session's context is independent of the HTTP request — the run
	// continues even after the POST returns.
	ctx, cancel := context.WithCancel(context.Background())
	sess := s.mgr.Create(req.Goal, req.Model, reg.Workdir(), cancel)

	go func() {
		defer cancel()
		runErr := agent.Run(ctx, agent.Config{
			LLM:           client,
			Tools:         reg,
			Goal:          req.Goal,
			MaxIterations: req.MaxIterations,
			Emit:          sess.Append,
		})
		// Run always emits a terminal Done event itself for the normal
		// happy/cancel paths. If something escaped without one, synthesise it
		// here so subscribers always see a terminator.
		if runErr != nil && !sess.Done() {
			sess.Append(agent.Event{
				Type:   agent.EventDone,
				TimeMS: time.Now().UnixMilli(),
				Reason: agent.ReasonError,
				Text:   runErr.Error(),
			})
		} else if runErr == nil && !sess.Done() {
			sess.Append(agent.Event{
				Type:   agent.EventDone,
				TimeMS: time.Now().UnixMilli(),
				Reason: agent.ReasonFinished,
			})
		}
	}()

	writeJSON(w, http.StatusOK, runResponse{SessionID: sess.ID})
}

// ---- sessions list / get --------------------------------------------------

type sessionSummary struct {
	ID      string `json:"id"`
	Goal    string `json:"goal"`
	Model   string `json:"model"`
	Workdir string `json:"workdir"`
	Done    bool   `json:"done"`
}

func summarize(s *Session) sessionSummary {
	return sessionSummary{ID: s.ID, Goal: s.Goal, Model: s.Model, Workdir: s.Workdir, Done: s.Done()}
}

func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	all := s.mgr.List()
	out := make([]sessionSummary, 0, len(all))
	for _, sess := range all {
		out = append(out, summarize(sess))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, summarize(sess))
}

// ---- SSE events -----------------------------------------------------------

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	history, ch, unsub := sess.Subscribe()
	defer unsub()

	// Replay history first so a late-connecting client sees the full run.
	for _, e := range history {
		if !writeSSEEvent(w, e) {
			return
		}
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !writeSSEEvent(w, e) {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, e agent.Event) bool {
	data, err := json.Marshal(e)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
	return err == nil
}

// ---- cancel ---------------------------------------------------------------

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	sess.Cancel()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- helpers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func httpError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Truncate(time.Millisecond))
	})
}
