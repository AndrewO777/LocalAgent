package server

import (
	"bufio"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrew/localagent/internal/agent"
)

func timeBoundedCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// readSSE consumes an event-stream until it sees `count` `event:` lines or
// the deadline expires. Returns the event-type lines (e.g. "iteration",
// "done") in order.
func readSSE(t *testing.T, body *bufio.Reader, count int, deadline time.Time) []string {
	t.Helper()
	var got []string
	for len(got) < count && time.Now().Before(deadline) {
		line, err := body.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event: ") {
			got = append(got, strings.TrimPrefix(line, "event: "))
		}
	}
	return got
}

func TestHandleEvents_FinishedSessionReplaysAllAndCloses(t *testing.T) {
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "finished", true, []agent.Event{
		{Type: agent.EventIteration, TimeMS: 1, Iter: 1},
		{Type: agent.EventModelText, TimeMS: 2, Text: "hi"},
	})

	req := httptest.NewRequest("GET", "/api/sessions/"+sess.ID+"/events", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// For a finished session, Subscribe returns a closed channel, so the
	// handler replays history then returns immediately.
	body := bufio.NewReader(w.Body)
	got := readSSE(t, body, 5, time.Now().Add(2*time.Second))

	// We expect: iteration, model_text, done. Order matters.
	want := []string{"iteration", "model_text", "done"}
	if !equalStrings(got, want) {
		t.Errorf("event types: got %v want %v", got, want)
	}
}

func TestHandleEvents_SkipsPriorDoneOnRunningSession(t *testing.T) {
	// Reproduces the continue-replay scenario: a session has events from
	// a previous run (including its done event), then was Reopen'd for a
	// continuation. The SSE handler must NOT replay the stale done event
	// — clients treat done as "stream finished" and would close early.
	srv := newTestServer(t)
	sess := preloadSession(t, srv, "finished", true, []agent.Event{
		{Type: agent.EventIteration, TimeMS: 1, Iter: 1},
	})
	// Now reopen as if for /continue and add a new iteration so there's
	// something current to see.
	sess.Reopen(func() {})
	sess.Append(agent.Event{Type: agent.EventIteration, TimeMS: 3, Iter: 2})

	// Run the handler; since the session is now running again, the body
	// stays open until the request context is canceled. We use a request
	// with a short context to bound the read.
	req := httptest.NewRequest("GET", "/api/sessions/"+sess.ID+"/events", nil)
	ctx, cancel := timeBoundedCtx(150 * time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.Routes().ServeHTTP(w, req)
		close(done)
	}()
	<-done

	body := bufio.NewReader(w.Body)
	got := readSSE(t, body, 10, time.Now().Add(time.Second))

	// We should see the two iteration events but NOT a `done` (the prior
	// one is filtered, no new one happened).
	if !containsString(got, "iteration") {
		t.Errorf("expected at least one iteration event, got %v", got)
	}
	if containsString(got, "done") {
		t.Errorf("prior done event leaked into replay; got %v", got)
	}
}

func TestHandleEvents_404ForUnknown(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "GET", "/api/sessions/deadbeef/events", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleEvents_BadIDIs400(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, srv.Routes(), "GET", "/api/sessions/$$$/events", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- helpers --------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
