package agent

import (
	"strings"
	"testing"
)

func TestValidateTodos_HappyPath(t *testing.T) {
	in := []Todo{
		{Content: "Research existing solutions", Status: TodoCompleted},
		{Content: "Scaffold the route handler", Status: TodoInProgress},
		{Content: "Add tests", Status: TodoPending},
	}
	out, err := validateTodos(in)
	if err != nil {
		t.Fatalf("validateTodos: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("length: got %d want 3", len(out))
	}
	for i, todo := range out {
		if todo.Content != in[i].Content {
			t.Errorf("[%d] content: got %q want %q", i, todo.Content, in[i].Content)
		}
	}
}

func TestValidateTodos_TrimsContent(t *testing.T) {
	in := []Todo{{Content: "   leading and trailing space  ", Status: TodoPending}}
	out, err := validateTodos(in)
	if err != nil {
		t.Fatalf("validateTodos: %v", err)
	}
	if out[0].Content != "leading and trailing space" {
		t.Errorf("content not trimmed: %q", out[0].Content)
	}
}

func TestValidateTodos_RejectsEmptyContent(t *testing.T) {
	in := []Todo{{Content: "   ", Status: TodoPending}}
	if _, err := validateTodos(in); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestValidateTodos_RejectsUnknownStatus(t *testing.T) {
	in := []Todo{{Content: "x", Status: "blocked"}}
	_, err := validateTodos(in)
	if err == nil {
		t.Fatal("expected error for unknown status")
	}
	if !strings.Contains(err.Error(), "unknown status") {
		t.Errorf("error should mention 'unknown status', got %q", err.Error())
	}
}

func TestValidateTodos_RejectsMissingStatus(t *testing.T) {
	in := []Todo{{Content: "x", Status: ""}}
	if _, err := validateTodos(in); err == nil {
		t.Fatal("expected error for empty status")
	}
}

func TestValidateTodos_RejectsTwoInProgress(t *testing.T) {
	in := []Todo{
		{Content: "a", Status: TodoInProgress},
		{Content: "b", Status: TodoInProgress},
	}
	_, err := validateTodos(in)
	if err == nil {
		t.Fatal("expected error for multiple in_progress")
	}
	if !strings.Contains(err.Error(), "in_progress") {
		t.Errorf("error should mention in_progress, got %q", err.Error())
	}
}

func TestValidateTodos_EmptyListAllowed(t *testing.T) {
	out, err := validateTodos(nil)
	if err != nil {
		t.Fatalf("empty list should be allowed: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty list should yield empty result, got %d", len(out))
	}
}

func TestRenderTodoSummary_AllStatuses(t *testing.T) {
	out := renderTodoSummary([]Todo{
		{Content: "a", Status: TodoCompleted},
		{Content: "b", Status: TodoInProgress},
		{Content: "c", Status: TodoPending},
	})
	for _, want := range []string{"1 completed", "1 in progress", "1 pending", "[x] a", "[>] b", "[ ] c"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in summary:\n%s", want, out)
		}
	}
}

func TestRenderTodoSummary_AllDoneIncludesFinishHint(t *testing.T) {
	out := renderTodoSummary([]Todo{
		{Content: "a", Status: TodoCompleted},
		{Content: "b", Status: TodoCompleted},
	})
	if !strings.Contains(out, "All todos complete") {
		t.Errorf("should include finish hint when all completed; got:\n%s", out)
	}
	if !strings.Contains(out, "finish") {
		t.Errorf("should mention finish tool; got:\n%s", out)
	}
}

func TestRenderTodoSummary_EmptyList(t *testing.T) {
	out := renderTodoSummary(nil)
	if !strings.Contains(out, "cleared") {
		t.Errorf("empty list summary should say 'cleared', got %q", out)
	}
}
