package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/andrew/localagent/internal/tools"
)

// TodoStatus is the lifecycle state of a single todo item.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// Todo is one entry in the agent's plan. The status field drives UI rendering
// (empty circle / spinner / checkmark) and gives the model a way to mark
// progress without us inventing IDs.
type Todo struct {
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// newUpdateTodosTool returns the update_todos tool. The handler validates
// each entry, emits a todo_update event with the full new list, and returns
// a confirmation summarising the state. The agent's surrounding session
// captures the event in its Append callback and stores it on the session.
//
// Design choice: a single "replace the whole list" operation instead of
// add/remove/mark verbs. This way the model never has to remember indices
// across turns — it just emits the desired state.
func newUpdateTodosTool(emit func(Event)) tools.Tool {
	return tools.Tool{
		Name: "update_todos",
		Description: `Maintain the agent's plan as a list of todo items. Each call REPLACES the full list — pass every todo you want to keep, including completed ones.

Required argument shape (a single object with a "todos" ARRAY):

{
  "todos": [
    {"content": "Read internal/server/server.go", "status": "completed"},
    {"content": "Add /healthz handler", "status": "in_progress"},
    {"content": "Write a test for healthz", "status": "pending"},
    {"content": "Run go test ./...", "status": "pending"}
  ]
}

Rules:
- Call this once at the very start of the run with 3-8 concrete milestones.
- Call again after each milestone to flip the current todo to "completed" and the next to "in_progress".
- Status values are exactly: "pending" | "in_progress" | "completed".
- Exactly one todo should be in_progress at a time.
- Add new todos as work emerges; don't try to predict everything up front.
- When every todo is completed, call finish with a summary to end the run.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "The full new todo list. Each item is an object with `content` (string) and `status` (one of pending|in_progress|completed).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{
								"type":        "string",
								"description": "What needs to be done. Short, imperative phrase (e.g. 'Add /healthz endpoint').",
							},
							"status": map[string]any{
								"type":        "string",
								"enum":        []string{"pending", "in_progress", "completed"},
								"description": "Lifecycle state of this todo.",
							},
						},
						"required": []string{"content", "status"},
					},
				},
			},
			"required": []string{"todos"},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			todos, err := parseTodosArgs(args)
			if err != nil {
				return "", err
			}
			cleaned, warnings, err := validateTodos(todos)
			if err != nil {
				return "", err
			}
			ev := newEvent(EventTodoUpdate)
			ev.Todos = cleaned
			emit(ev)
			result := renderTodoSummary(cleaned)
			if len(warnings) > 0 {
				var b strings.Builder
				b.WriteString(result)
				b.WriteString("\n\nNotes (consider correcting on your next update_todos call):")
				for _, w := range warnings {
					b.WriteString("\n- ")
					b.WriteString(w)
				}
				result = b.String()
			}
			return result, nil
		},
	}
}

// parseTodosArgs accepts the canonical {"todos":[{"content","status"}]} shape
// AND several common variations that smaller models routinely produce:
//
//   - synonym keys for the list ("items", "tasks", "list", "todo_list", "plan")
//   - synonym keys per item ("text", "title", "name", "description", "todo", "task")
//   - synonym keys for status ("state"); status values normalized to canonical form
//   - plain string items, e.g. ["task A", "task B"], treated as pending
//   - a single-todo flat shape: {"content": "...", "status": "..."} → list of one
//   - a status-grouped shape: {"pending": [...], "in_progress": [...], ...}
//
// Being tolerant here means update_todos succeeds on the first try far more
// often, instead of forcing the model into a multi-retry self-correction loop
// that burns iterations and confuses the user.
func parseTodosArgs(args string) ([]Todo, error) {
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}

	// (1) Standard shape: a list under one of several keys.
	for _, key := range []string{"todos", "items", "tasks", "list", "todo_list", "plan"} {
		if v, ok := raw[key]; ok {
			out, err := extractTodoArray(v, TodoPending)
			if err != nil {
				return nil, fmt.Errorf(`field %q: %w`, key, err)
			}
			return out, nil
		}
	}

	// (2) Status-grouped shape: keys are statuses, values are arrays of items.
	//     e.g. {"pending":[...], "in_progress":[...], "completed":[...]}
	statusKeys := []string{"pending", "in_progress", "completed", "todo", "doing", "done"}
	statusGrouped := false
	for _, k := range statusKeys {
		if _, ok := raw[k]; ok {
			statusGrouped = true
			break
		}
	}
	if statusGrouped {
		var out []Todo
		for _, k := range statusKeys {
			v, ok := raw[k]
			if !ok {
				continue
			}
			status := normalizeStatus(k)
			items, err := extractTodoArray(v, status)
			if err != nil {
				continue // skip unparseable groups rather than fail the whole call
			}
			// Force the group's status onto each item even if it had its own.
			for i := range items {
				items[i].Status = status
			}
			out = append(out, items...)
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// (3) Single-todo flat shape: the model thought update_todos takes ONE todo.
	if t, ok := todoFromMap(raw, TodoPending); ok {
		return []Todo{t}, nil
	}

	return nil, fmt.Errorf(`could not find a todos list in arguments — expected {"todos":[{"content":"...","status":"pending|in_progress|completed"}, ...]}`)
}

// extractTodoArray accepts a value that should be an array of todos and
// normalizes each entry. Each entry may be a string (treated as content with
// defaultStatus) or an object that's parsed via todoFromMap.
func extractTodoArray(v any, defaultStatus TodoStatus) ([]Todo, error) {
	arr, ok := v.([]any)
	if !ok {
		// One special case: a single object passed where an array was
		// expected. Treat as a one-element list.
		if m, ok := v.(map[string]any); ok {
			if t, ok := todoFromMap(m, defaultStatus); ok {
				return []Todo{t}, nil
			}
		}
		return nil, fmt.Errorf("expected an array, got %T", v)
	}
	out := make([]Todo, 0, len(arr))
	for i, item := range arr {
		switch x := item.(type) {
		case string:
			content := strings.TrimSpace(x)
			if content == "" {
				return nil, fmt.Errorf("item %d: empty string", i)
			}
			out = append(out, Todo{Content: content, Status: defaultStatus})
		case map[string]any:
			t, ok := todoFromMap(x, defaultStatus)
			if !ok {
				return nil, fmt.Errorf("item %d: object has no recognizable content field (expected content/text/title/name/description/todo/task)", i)
			}
			out = append(out, t)
		default:
			return nil, fmt.Errorf("item %d: unexpected type %T", i, item)
		}
	}
	return out, nil
}

// todoFromMap extracts a Todo from a JSON object, trying common synonyms for
// the content and status fields. Returns ok=false if no content-like field
// is present (so the single-todo shorthand can be rejected when the model
// passed a list-shaped object that just happened to be at the top level).
func todoFromMap(m map[string]any, defaultStatus TodoStatus) (Todo, bool) {
	content := ""
	for _, key := range []string{"content", "text", "title", "name", "description", "todo", "task", "item"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				content = strings.TrimSpace(s)
				break
			}
		}
	}
	if content == "" {
		return Todo{}, false
	}
	status := defaultStatus
	for _, key := range []string{"status", "state"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				status = normalizeStatus(s)
				break
			}
			// Some models pass booleans for status: {"completed": true}.
			if b, ok := v.(bool); ok && b {
				status = TodoCompleted
			}
		}
	}
	// "completed":true / "in_progress":true / "done":true sometimes appears
	// as a sibling boolean instead of an explicit status field.
	if status == defaultStatus {
		for k, v := range m {
			if b, ok := v.(bool); ok && b {
				switch strings.ToLower(k) {
				case "completed", "complete", "done", "finished":
					status = TodoCompleted
				case "in_progress", "inprogress", "doing", "active", "wip":
					status = TodoInProgress
				}
			}
		}
	}
	return Todo{Content: content, Status: status}, true
}

// normalizeStatus maps any common synonym to one of our three canonical
// statuses. Unknown input falls back to pending — better than erroring,
// because the model can self-correct on the next call.
func normalizeStatus(s string) TodoStatus {
	k := strings.ToLower(strings.TrimSpace(s))
	k = strings.ReplaceAll(k, "-", "_")
	k = strings.ReplaceAll(k, " ", "_")
	switch k {
	case "completed", "complete", "done", "finished", "closed", "resolved":
		return TodoCompleted
	case "in_progress", "inprogress", "doing", "active", "wip", "working", "started":
		return TodoInProgress
	case "pending", "todo", "open", "not_started", "queued", "waiting", "":
		return TodoPending
	}
	return TodoPending
}

// validateTodos returns a cleaned copy of `in` (content trimmed, status
// normalized) plus a list of soft warnings the model can see in the tool
// result. We only return a hard error for things we cannot represent
// (empty content); everything else falls back to a sensible default so the
// agent loop doesn't grind on retries.
//
// An empty list is allowed — the model can clear the plan if the goal pivots.
func validateTodos(in []Todo) ([]Todo, []string, error) {
	out := make([]Todo, 0, len(in))
	var warnings []string
	inProgressCount := 0
	for i, t := range in {
		content := strings.TrimSpace(t.Content)
		if content == "" {
			return nil, nil, fmt.Errorf("todo %d: content is empty", i)
		}
		status := t.Status
		switch status {
		case TodoPending, TodoInProgress, TodoCompleted:
			// already canonical
		case "":
			warnings = append(warnings, fmt.Sprintf("todo %d (%q): status was empty, treated as pending", i, content))
			status = TodoPending
		default:
			// Try to coerce one more time in case caller passed something
			// like "Pending" or "DONE".
			coerced := normalizeStatus(string(status))
			warnings = append(warnings, fmt.Sprintf("todo %d (%q): unknown status %q, treated as %q", i, content, status, coerced))
			status = coerced
		}
		if status == TodoInProgress {
			inProgressCount++
		}
		out = append(out, Todo{Content: content, Status: status})
	}
	if inProgressCount > 1 {
		warnings = append(warnings, fmt.Sprintf("%d todos are in_progress — convention is exactly one at a time. Consider marking the others pending on your next update_todos call.", inProgressCount))
	}
	return out, warnings, nil
}

// renderTodoSummary builds the human-readable confirmation returned to the
// model after a successful update. The model echoes this in its message
// history, which keeps the latest plan visible at the bottom of context
// without us re-injecting it every turn.
func renderTodoSummary(todos []Todo) string {
	if len(todos) == 0 {
		return "Todo list cleared (no items)."
	}
	var b strings.Builder
	pending, in, done := 0, 0, 0
	for _, t := range todos {
		switch t.Status {
		case TodoPending:
			pending++
		case TodoInProgress:
			in++
		case TodoCompleted:
			done++
		}
	}
	fmt.Fprintf(&b, "Todos updated. %d total — %d completed, %d in progress, %d pending.\n",
		len(todos), done, in, pending)
	for i, t := range todos {
		mark := "[ ]"
		switch t.Status {
		case TodoInProgress:
			mark = "[>]"
		case TodoCompleted:
			mark = "[x]"
		}
		fmt.Fprintf(&b, "%d. %s %s\n", i+1, mark, t.Content)
	}
	if done == len(todos) {
		b.WriteString("\nAll todos complete. Call finish with a summary to end the run.")
	}
	return strings.TrimRight(b.String(), "\n")
}
