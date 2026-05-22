package agent

import (
	"context"
	"encoding/json"
	"errors"
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
		Description: "Maintain the agent's plan as a list of todo items. " +
			"Call this once at the very start of the run with a list of 3-8 concrete " +
			"milestones for the user's goal. Then call again after each milestone to " +
			"update statuses (mark the current one `completed` and the next one " +
			"`in_progress`). Each call REPLACES the full list — pass every todo " +
			"you want to keep, not just the changed ones.\n\n" +
			"Rules:\n" +
			"- Exactly one todo should be `in_progress` at a time.\n" +
			"- Mark a todo `completed` only when it's fully done (tests pass, file written, etc).\n" +
			"- Add new todos as they emerge; don't try to predict everything up front.\n" +
			"- When every todo is `completed`, call `finish` with a summary to end the run.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "The full new todo list. Each item is an object with content and status.",
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
			var in struct {
				Todos []Todo `json:"todos"`
			}
			dec := json.NewDecoder(strings.NewReader(args))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			cleaned, err := validateTodos(in.Todos)
			if err != nil {
				return "", err
			}

			ev := newEvent(EventTodoUpdate)
			ev.Todos = cleaned
			emit(ev)

			return renderTodoSummary(cleaned), nil
		},
	}
}

// validateTodos checks each todo's status is a known value and content is
// non-empty. Returns a cleaned copy (content trimmed). Allows an empty list
// (the model can clear todos by passing []) — that's intentional, in case
// the goal pivots mid-run.
func validateTodos(in []Todo) ([]Todo, error) {
	out := make([]Todo, 0, len(in))
	inProgressCount := 0
	for i, t := range in {
		content := strings.TrimSpace(t.Content)
		if content == "" {
			return nil, fmt.Errorf("todo %d: content is empty", i)
		}
		switch t.Status {
		case TodoPending, TodoInProgress, TodoCompleted:
			// ok
		case "":
			return nil, fmt.Errorf("todo %d (%q): status is required", i, content)
		default:
			return nil, fmt.Errorf("todo %d (%q): unknown status %q — must be pending|in_progress|completed", i, content, t.Status)
		}
		if t.Status == TodoInProgress {
			inProgressCount++
		}
		out = append(out, Todo{Content: content, Status: t.Status})
	}
	if inProgressCount > 1 {
		// Soft warning baked into the error so the model self-corrects on
		// retry. We don't reject — it's the model's plan, not ours — but
		// we'd rather it follow the convention.
		return nil, errors.New("more than one todo is in_progress; only one should be in_progress at a time. Re-issue update_todos with at most one in_progress entry.")
	}
	return out, nil
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
