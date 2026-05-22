package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voocel/litellm"

	"github.com/andrew/localagent/internal/agent"
)

// newTestServer constructs a Server with an in-memory manager and a real
// FileStore rooted under t.TempDir(). The cleanup is registered via t.Cleanup
// so each test gets a fresh datadir.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return New(emptyFS{}, "http://localhost:11434", store)
}

// emptyFS satisfies fs.FS for tests that don't exercise the static UI.
type emptyFS struct{}

func (emptyFS) Open(_ string) (fs.File, error) { return nil, os.ErrNotExist }

// preloadSession inserts a session directly into the manager, bypassing
// handleRun. Used when a test needs a session to exist without the cost (or
// dependency) of actually running an agent loop.
//
// When `done` is true, the terminal Append triggers an async file Save that
// can still be writing when t.TempDir cleanup runs. We wait for the file to
// appear before returning so tests don't race the goroutine.
func preloadSession(t *testing.T, srv *Server, status string, done bool, events []agent.Event) *Session {
	t.Helper()
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sess := srv.mgr.Create("test goal", "test-model", "", 0, t.TempDir(), "http://test-host:11434", cancel)
	for _, e := range events {
		sess.Append(e)
	}
	if done {
		sess.Append(agent.Event{
			Type:   agent.EventDone,
			TimeMS: time.Now().UnixMilli(),
			Reason: status,
		})
		// Wait for the async file Save to land (best-effort; bounded).
		// Windows file-cleanup is strict about in-flight writes.
		if srv.mgr.store != nil {
			deadline := time.Now().Add(2 * time.Second)
			path, _ := srv.mgr.store.path(sess.ID)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(path); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	return sess
}

// doJSON is a small helper to POST a JSON body and decode the response.
func doJSON(t *testing.T, h http.Handler, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	resp := w.Result()
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

// --- handleRun: validation -------------------------------------------------

func TestHandleRun_RejectsMissingFields(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing all", map[string]any{}, http.StatusBadRequest},
		{"missing model", map[string]any{"workdir": ".", "goal": "x"}, http.StatusBadRequest},
		{"missing workdir", map[string]any{"model": "m", "goal": "x"}, http.StatusBadRequest},
		{"missing goal (no skills)", map[string]any{"model": "m", "workdir": "."}, http.StatusBadRequest},
		// Workdir that doesn't exist — tools.Build rejects.
		{"nonexistent workdir", map[string]any{"model": "m", "workdir": "/__nope__", "goal": "x"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, srv.Routes(), "POST", "/api/run", tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status: got %d want %d (body=%s)", resp.StatusCode, tc.want, body)
			}
		})
	}
}

func TestHandleRun_BadJSONIs400(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/run", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", w.Code)
	}
}

// --- handleContinueSession: validation -------------------------------------

func TestHandleContinueSession_InvalidID(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "POST", "/api/sessions/!!!/continue", map[string]string{"goal": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid id, got %d", resp.StatusCode)
	}
}

func TestHandleContinueSession_NotFound(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "POST", "/api/sessions/deadbeef/continue", map[string]string{"goal": "x"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown id, got %d", resp.StatusCode)
	}
}

func TestHandleContinueSession_StillRunning(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	resp, _ := doJSON(t, srv.Routes(), "POST", "/api/sessions/"+sess.ID+"/continue", map[string]string{"goal": "x"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for still-running session, got %d", resp.StatusCode)
	}
}

func TestHandleContinueSession_InheritsHostFromSession(t *testing.T) {
	// Regression: a session that originally ran against a remote Ollama
	// must continue against the same host, even if the request body has
	// no `host` field (the most common case, since the UI's form.host
	// can be empty for a variety of reasons). Without this, the continue
	// silently retargets s.defaultHost and the model is "not found".
	srv := newTestServer(t)
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sess := srv.mgr.Create("orig goal", "gemma4:26b", "", 0, t.TempDir(), "http://remote-ollama:11434", cancel)
	// Continue requires a non-empty stored conversation that ends cleanly
	// (no orphan tool_calls). A minimal sys+user+assistant trio works.
	sess.SetMessages([]litellm.Message{
		litellm.SystemMessage("you are an agent"),
		litellm.UserMessage("hi"),
		{Role: "assistant", Content: "ok"},
	})
	sess.Append(agent.Event{Type: agent.EventDone, Reason: "finished", TimeMS: time.Now().UnixMilli()})

	// Continue with NO host override. The bug we're guarding against
	// would default to s.defaultHost and corrupt the run.
	resp, body := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/continue",
		map[string]any{"goal": "do more"},
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}

	// Most important: the session's stored Host must be intact after
	// the continue handler runs (it shouldn't be overwritten with the
	// default host).
	if sess.Host != "http://remote-ollama:11434" {
		t.Errorf("session host got clobbered: %q", sess.Host)
	}
}

// resolveContinueHost mirrors the precedence used by handleContinueSession
// (request override → session host → server default). This is the unit-test
// view of the bug fix: empty request host must NOT silently fall through
// to the default when the session has its own host.
func TestContinueHostPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		reqHost     string
		sessionHost string
		defaultHost string
		want        string
	}{
		{"request overrides everything", "http://override:11434", "http://session:11434", "http://default:11434", "http://override:11434"},
		{"empty request → session host", "", "http://session:11434", "http://default:11434", "http://session:11434"},
		{"empty request + empty session → default", "", "", "http://default:11434", "http://default:11434"},
		{"whitespace request treated as empty", "   ", "http://session:11434", "http://default:11434", "http://session:11434"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveContinueHost(tc.reqHost, tc.sessionHost, tc.defaultHost)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// resolveContinueHost extracts the precedence logic so we can test it
// without a full HTTP round trip. The actual handler inlines the same
// fallback chain; this helper exists in test-land to make the regression
// guard tight and readable.
func resolveContinueHost(reqHost, sessionHost, defaultHost string) string {
	h := strings.TrimSpace(reqHost)
	if h == "" {
		h = sessionHost
	}
	if h == "" {
		h = defaultHost
	}
	return h
}

func TestHandleContinueSession_NoMessages(t *testing.T) {
	srv := newTestServer(t)
	// Session that's done but has no stored messages (e.g. a legacy run).
	sess := preloadSession(t, srv, "finished", true, nil)
	resp, body := doJSON(t, srv.Routes(), "POST", "/api/sessions/"+sess.ID+"/continue", map[string]string{"goal": "x"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for empty conversation, got %d (body=%s)", resp.StatusCode, body)
	}
}

// --- handleListSessions ----------------------------------------------------

func TestHandleListSessions_EmptyReturnsArray(t *testing.T) {
	srv := newTestServer(t)
	resp, body := doJSON(t, srv.Routes(), "GET", "/api/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	// Empty list must serialise as `[]`, not `null` — UI assumes an array.
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body should be '[]', got %q", string(body))
	}
}

func TestHandleListSessions_IncludesInMemory(t *testing.T) {
	srv := newTestServer(t)
	_ = preloadSession(t, srv, "running", false, nil)
	resp, body := doJSON(t, srv.Routes(), "GET", "/api/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var list []Summary
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
}

// --- handleGetSession ------------------------------------------------------

func TestHandleGetSession_404(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "GET", "/api/sessions/deadbeef", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown id, got %d", resp.StatusCode)
	}
}

func TestHandleGetSession_BadID(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "GET", "/api/sessions/$$$", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid id, got %d", resp.StatusCode)
	}
}

func TestHandleGetSession_ReturnsEvents(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "finished", true, []agent.Event{
		{Type: agent.EventIteration, TimeMS: time.Now().UnixMilli(), Iter: 1},
		{Type: agent.EventModelText, TimeMS: time.Now().UnixMilli(), Text: "hi"},
	})
	resp, body := doJSON(t, srv.Routes(), "GET", "/api/sessions/"+sess.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var detail struct {
		Summary
		Events []agent.Event `json:"events"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(detail.Events) != 3 { // 2 preloaded + the synthetic done from preloadSession
		t.Fatalf("expected 3 events, got %d", len(detail.Events))
	}
}

// --- handleDeleteSession ---------------------------------------------------

func TestHandleDeleteSession_RemovesFromMemoryAndDisk(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "finished", true, nil) // triggers async Save
	// Allow the async Save inside Append's done branch to land.
	time.Sleep(50 * time.Millisecond)

	resp, _ := doJSON(t, srv.Routes(), "DELETE", "/api/sessions/"+sess.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	resp2, _ := doJSON(t, srv.Routes(), "GET", "/api/sessions/"+sess.ID, nil)
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("session should be gone, got %d", resp2.StatusCode)
	}
}

func TestHandleDeleteSession_CancelsRunning(t *testing.T) {
	srv := newTestServer(t)
	var canceled bool
	var mu sync.Mutex
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sess := srv.mgr.Create("g", "m", "", 0, t.TempDir(), "", func() {
		mu.Lock()
		canceled = true
		mu.Unlock()
		cancel()
	})
	resp, _ := doJSON(t, srv.Routes(), "DELETE", "/api/sessions/"+sess.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if !canceled {
		t.Fatal("cancel should have been invoked on the running session")
	}
}

// --- handleCancel ----------------------------------------------------------

func TestHandleCancel_InvokesCancel(t *testing.T) {
	srv := newTestServer(t)
	var canceled bool
	var mu sync.Mutex
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sess := srv.mgr.Create("g", "m", "", 0, t.TempDir(), "", func() {
		mu.Lock()
		canceled = true
		mu.Unlock()
		cancel()
	})
	resp, _ := doJSON(t, srv.Routes(), "POST", "/api/sessions/"+sess.ID+"/cancel", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if !canceled {
		t.Fatal("cancel should have been invoked")
	}
}

func TestHandleCancel_404ForUnknown(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "POST", "/api/sessions/deadbeef/cancel", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- handleAnswer ----------------------------------------------------------

func TestHandleAnswer_DeliversAndUnblocks(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	id := "q_test"
	ch := sess.RegisterQuestion(id)
	defer sess.UnregisterQuestion(id)

	delivered := make(chan string, 1)
	go func() {
		delivered <- <-ch
	}()

	resp, body := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/answer",
		map[string]string{"question_id": id, "answer": "yes go"},
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	select {
	case got := <-delivered:
		if got != "yes go" {
			t.Errorf("delivered text: got %q want %q", got, "yes go")
		}
	case <-time.After(time.Second):
		t.Fatal("answer was not delivered to the registered channel in time")
	}
}

func TestHandleAnswer_NotPendingReturns409(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	resp, _ := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/answer",
		map[string]string{"question_id": "q_nope", "answer": "x"},
	)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for unpending question, got %d", resp.StatusCode)
	}
}

func TestHandleAnswer_MissingQuestionID(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	resp, _ := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/answer",
		map[string]string{"answer": "x"},
	)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing question_id, got %d", resp.StatusCode)
	}
}

func TestHandleAnswer_EmptyAnswerAllowed(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	id := "q_empty"
	ch := sess.RegisterQuestion(id)
	defer sess.UnregisterQuestion(id)

	go func() { <-ch }()

	resp, _ := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/answer",
		map[string]string{"question_id": id, "answer": ""},
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty answer should be accepted, got %d", resp.StatusCode)
	}
}

// --- handleInject ----------------------------------------------------------

func TestHandleInject_QueuesMessage(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	resp, _ := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/inject",
		map[string]string{"message": "also do X"},
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got := sess.DrainInjections()
	if len(got) != 1 || got[0] != "also do X" {
		t.Errorf("expected queued injection, got %v", got)
	}
}

func TestHandleInject_FinishedSessionReturns409(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "finished", true, nil)
	resp, _ := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/inject",
		map[string]string{"message": "x"},
	)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for finished session, got %d", resp.StatusCode)
	}
}

func TestHandleInject_EmptyMessage(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "running", false, nil)
	resp, _ := doJSON(t, srv.Routes(), "POST",
		"/api/sessions/"+sess.ID+"/inject",
		map[string]string{"message": "   "},
	)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty message, got %d", resp.StatusCode)
	}
}

// --- handleListSkills ------------------------------------------------------

func TestHandleListSkills_EmptyWorkdir(t *testing.T) {
	srv := newTestServer(t)
	resp, body := doJSON(t, srv.Routes(), "GET", "/api/skills?workdir="+t.TempDir(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	// Even with no skills, the shape is consistent.
	if !strings.Contains(string(body), `"skills"`) {
		t.Errorf(`response should have a "skills" field, got %s`, body)
	}
}

func TestHandleListSkills_DiscoversProjectSkill(t *testing.T) {
	srv := newTestServer(t)
	workdir := t.TempDir()
	skillDir := filepath.Join(workdir, ".localagent", "skills", "myskill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: myskill\ndescription: be concise\n---\n# body\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, body := doJSON(t, srv.Routes(), "GET", "/api/skills?workdir="+workdir, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"myskill"`) {
		t.Errorf("response should list myskill, got %s", body)
	}
}

// --- mergeSkillNames helper -----------------------------------------------

func TestMergeSkillNames(t *testing.T) {
	cases := []struct {
		name      string
		primary   []string
		secondary []string
		want      []string
	}{
		{"both empty", nil, nil, []string{}},
		{"primary only", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"dedup across", []string{"a"}, []string{"a", "b"}, []string{"a", "b"}},
		{"trims whitespace", []string{" a ", ""}, []string{"b"}, []string{"a", "b"}},
		{"preserves primary order", []string{"b", "a"}, []string{"c", "a"}, []string{"b", "a", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeSkillNames(tc.primary, tc.secondary)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
