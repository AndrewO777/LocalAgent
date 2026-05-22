package server

import (
	"testing"
	"time"
)

func TestSession_AnswerDeliversToWaitingHandler(t *testing.T) {
	s := &Session{}
	ch := s.RegisterQuestion("q1")

	done := make(chan string, 1)
	go func() {
		done <- <-ch
	}()

	if !s.Answer("q1", "go ahead") {
		t.Fatal("Answer returned false for a registered question")
	}
	select {
	case got := <-done:
		if got != "go ahead" {
			t.Errorf("got %q, want %q", got, "go ahead")
		}
	case <-time.After(time.Second):
		t.Fatal("answer was not delivered within 1s")
	}
}

func TestSession_AnswerUnknownQuestionReturnsFalse(t *testing.T) {
	s := &Session{}
	if s.Answer("nope", "ignored") {
		t.Fatal("Answer should return false for an unknown question id")
	}
}

func TestSession_DoubleAnswerSecondReturnsFalse(t *testing.T) {
	s := &Session{}
	ch := s.RegisterQuestion("q1")
	if !s.Answer("q1", "first") {
		t.Fatal("first Answer should succeed")
	}
	if s.Answer("q1", "second") {
		t.Fatal("second Answer should fail (question was removed on first delivery)")
	}
	// First reader should still see "first".
	got := <-ch
	if got != "first" {
		t.Errorf("got %q, want 'first'", got)
	}
}

func TestSession_UnregisterAfterCancel_NoLeak(t *testing.T) {
	s := &Session{}
	_ = s.RegisterQuestion("q1")
	if len(s.PendingQuestions()) != 1 {
		t.Fatal("expected 1 pending question after register")
	}
	s.UnregisterQuestion("q1")
	if len(s.PendingQuestions()) != 0 {
		t.Fatalf("expected 0 pending after unregister, got %v", s.PendingQuestions())
	}
	// Answer after unregister should be a no-op.
	if s.Answer("q1", "late") {
		t.Fatal("Answer to unregistered question should return false")
	}
}

func TestSession_AnswerAfterReaderGoneIsNotABlock(t *testing.T) {
	// Simulate the cancel-during-question case: reader has given up and
	// won't drain the channel. The 1-buffered channel + non-blocking send
	// must mean Answer still returns quickly. (If it blocked we'd hang
	// the HTTP handler waiting forever.)
	s := &Session{}
	_ = s.RegisterQuestion("q1")

	done := make(chan bool, 1)
	go func() {
		// First Answer fills the buffer.
		_ = s.Answer("q1", "first")
		// Re-register and don't drain. A second Answer must not block.
		_ = s.RegisterQuestion("q1")
		_ = s.Answer("q1", "second")
		// Try yet again — channel is full, sender is gone; non-blocking
		// send returns false rather than hanging.
		_ = s.RegisterQuestion("q1")
		ch2 := s.pendingQuestions["q1"]
		// Fill it so the next send hits the default branch.
		ch2 <- "filler"
		_ = s.Answer("q1", "third")
		done <- true
	}()

	select {
	case <-done:
		// good — none of the sends blocked
	case <-time.After(time.Second):
		t.Fatal("Answer blocked when reader was gone (regression)")
	}
}

func TestSession_ReopenClearsPending(t *testing.T) {
	s := &Session{}
	_ = s.RegisterQuestion("q1")
	if len(s.PendingQuestions()) != 1 {
		t.Fatal("setup: expected 1 pending")
	}
	s.Reopen(nil)
	if len(s.PendingQuestions()) != 0 {
		t.Fatalf("Reopen should clear pendingQuestions, got %v", s.PendingQuestions())
	}
}

// --- inject queue ----------------------------------------------------------

func TestSession_InjectAndDrain(t *testing.T) {
	s := &Session{}
	s.Inject("hello")
	s.Inject("more context")
	got := s.DrainInjections()
	if len(got) != 2 || got[0] != "hello" || got[1] != "more context" {
		t.Fatalf("unexpected drain result: %v", got)
	}
	// Second drain returns empty — queue was cleared.
	if got := s.DrainInjections(); len(got) != 0 {
		t.Fatalf("second drain should be empty, got %v", got)
	}
}

func TestSession_DrainEmptyQueueIsSafe(t *testing.T) {
	s := &Session{}
	if got := s.DrainInjections(); len(got) != 0 {
		t.Fatalf("empty session should drain to empty, got %v", got)
	}
}

func TestSession_ReopenClearsInjections(t *testing.T) {
	s := &Session{}
	s.Inject("queued for previous run")
	s.Reopen(nil)
	if got := s.DrainInjections(); len(got) != 0 {
		t.Fatalf("Reopen should drop pending injections, got %v", got)
	}
}
