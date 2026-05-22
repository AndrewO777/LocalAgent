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
	out, warns, err := validateTodos(in)
	if err != nil {
		t.Fatalf("validateTodos: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("happy path should have no warnings, got %v", warns)
	}
	if len(out) != 3 {
		t.Fatalf("length: got %d want 3", len(out))
	}
}

func TestValidateTodos_TrimsContent(t *testing.T) {
	in := []Todo{{Content: "   leading and trailing space  ", Status: TodoPending}}
	out, _, err := validateTodos(in)
	if err != nil {
		t.Fatalf("validateTodos: %v", err)
	}
	if out[0].Content != "leading and trailing space" {
		t.Errorf("content not trimmed: %q", out[0].Content)
	}
}

func TestValidateTodos_RejectsEmptyContent(t *testing.T) {
	in := []Todo{{Content: "   ", Status: TodoPending}}
	if _, _, err := validateTodos(in); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestValidateTodos_CoercesUnknownStatusWithWarning(t *testing.T) {
	in := []Todo{{Content: "x", Status: "blocked"}}
	out, warns, err := validateTodos(in)
	if err != nil {
		t.Fatalf("unknown status should be coerced, not errored: %v", err)
	}
	if out[0].Status != TodoPending {
		t.Errorf("unknown status should fall back to pending, got %q", out[0].Status)
	}
	if len(warns) == 0 {
		t.Error("expected a warning for the unknown status")
	}
}

func TestValidateTodos_MissingStatusBecomesPending(t *testing.T) {
	in := []Todo{{Content: "x", Status: ""}}
	out, warns, err := validateTodos(in)
	if err != nil {
		t.Fatalf("missing status should be tolerated: %v", err)
	}
	if out[0].Status != TodoPending {
		t.Errorf("expected pending, got %q", out[0].Status)
	}
	if len(warns) == 0 {
		t.Error("expected a warning for missing status")
	}
}

func TestValidateTodos_TwoInProgressGivesWarningNotError(t *testing.T) {
	in := []Todo{
		{Content: "a", Status: TodoInProgress},
		{Content: "b", Status: TodoInProgress},
	}
	out, warns, err := validateTodos(in)
	if err != nil {
		t.Fatalf("two in_progress should be tolerated: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("data should still come through, got %d items", len(out))
	}
	if len(warns) == 0 {
		t.Error("expected a warning about multiple in_progress")
	}
}

func TestValidateTodos_EmptyListAllowed(t *testing.T) {
	out, warns, err := validateTodos(nil)
	if err != nil {
		t.Fatalf("empty list should be allowed: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty list should yield empty result, got %d", len(out))
	}
	if len(warns) != 0 {
		t.Errorf("empty list should have no warnings, got %v", warns)
	}
}

// --- parseTodosArgs --------------------------------------------------------

func TestParseTodosArgs_Canonical(t *testing.T) {
	args := `{"todos":[{"content":"a","status":"pending"},{"content":"b","status":"in_progress"}]}`
	got, err := parseTodosArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0].Content != "a" || got[1].Status != TodoInProgress {
		t.Errorf("unexpected parse result: %#v", got)
	}
}

func TestParseTodosArgs_SynonymKeysForList(t *testing.T) {
	for _, key := range []string{"items", "tasks", "list", "todo_list", "plan"} {
		args := `{"` + key + `":[{"content":"a","status":"pending"}]}`
		got, err := parseTodosArgs(args)
		if err != nil {
			t.Errorf("key %q: %v", key, err)
			continue
		}
		if len(got) != 1 || got[0].Content != "a" {
			t.Errorf("key %q: unexpected result %#v", key, got)
		}
	}
}

func TestParseTodosArgs_SynonymKeysForContent(t *testing.T) {
	for _, key := range []string{"text", "title", "name", "description", "todo", "task"} {
		args := `{"todos":[{"` + key + `":"the work","status":"in_progress"}]}`
		got, err := parseTodosArgs(args)
		if err != nil {
			t.Errorf("content key %q: %v", key, err)
			continue
		}
		if len(got) != 1 || got[0].Content != "the work" || got[0].Status != TodoInProgress {
			t.Errorf("content key %q: unexpected result %#v", key, got)
		}
	}
}

func TestParseTodosArgs_PlainStringItems(t *testing.T) {
	// Model passes an array of plain strings — treat each as a pending todo.
	args := `{"todos":["task 1","task 2"]}`
	got, err := parseTodosArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0].Content != "task 1" || got[0].Status != TodoPending {
		t.Errorf("unexpected: %#v", got)
	}
}

func TestParseTodosArgs_SingleTodoFlatShape(t *testing.T) {
	// This is the exact misuse the user reported: the model called
	// update_todos with the fields of a single todo at the top level.
	cases := []string{
		`{"content":"task X","status":"in_progress"}`,
		`{"text":"task X","status":"in_progress"}`,
		`{"todo":"task X","status":"in_progress"}`,
		`{"description":"task X","status":"in_progress"}`,
	}
	for _, args := range cases {
		got, err := parseTodosArgs(args)
		if err != nil {
			t.Errorf("%s: %v", args, err)
			continue
		}
		if len(got) != 1 || got[0].Content != "task X" || got[0].Status != TodoInProgress {
			t.Errorf("%s: unexpected %#v", args, got)
		}
	}
}

func TestParseTodosArgs_StatusGroupedShape(t *testing.T) {
	args := `{
		"pending":[{"content":"c"}],
		"in_progress":[{"content":"b"}],
		"completed":[{"content":"a"}]
	}`
	got, err := parseTodosArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
	byContent := map[string]TodoStatus{}
	for _, t := range got {
		byContent[t.Content] = t.Status
	}
	if byContent["a"] != TodoCompleted || byContent["b"] != TodoInProgress || byContent["c"] != TodoPending {
		t.Errorf("status mapping wrong: %v", byContent)
	}
}

func TestParseTodosArgs_StatusSynonymsAndCase(t *testing.T) {
	cases := map[string]TodoStatus{
		"DONE":         TodoCompleted,
		"complete":     TodoCompleted,
		"finished":     TodoCompleted,
		"resolved":     TodoCompleted,
		"in progress":  TodoInProgress, // space, not underscore
		"in-progress":  TodoInProgress, // dash
		"doing":        TodoInProgress,
		"WIP":          TodoInProgress,
		"working":      TodoInProgress,
		"todo":         TodoPending,
		"open":         TodoPending,
		"not_started":  TodoPending,
		"unknown-junk": TodoPending, // fallback
	}
	for in, want := range cases {
		args := `{"todos":[{"content":"x","status":"` + in + `"}]}`
		got, err := parseTodosArgs(args)
		if err != nil {
			t.Errorf("status %q: %v", in, err)
			continue
		}
		if got[0].Status != want {
			t.Errorf("status %q: got %q want %q", in, got[0].Status, want)
		}
	}
}

func TestParseTodosArgs_StatusAsBooleanField(t *testing.T) {
	// Some models pass {"content":"x","completed":true}.
	args := `{"todos":[{"content":"x","completed":true}]}`
	got, err := parseTodosArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got[0].Status != TodoCompleted {
		t.Errorf("expected completed, got %q", got[0].Status)
	}
}

func TestParseTodosArgs_InvalidJSON(t *testing.T) {
	if _, err := parseTodosArgs("not json"); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseTodosArgs_NoRecognizableShape(t *testing.T) {
	if _, err := parseTodosArgs(`{"foo":"bar"}`); err == nil {
		t.Fatal("expected error when no todos shape can be found")
	}
}

func TestParseTodosArgs_EmptyArgs(t *testing.T) {
	// Calling update_todos with no arguments shouldn't crash.
	if _, err := parseTodosArgs(""); err == nil {
		t.Fatal("expected error for empty arguments")
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
