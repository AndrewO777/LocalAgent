package agent

import "time"

// EventType enumerates the kinds of events emitted during an agent run.
type EventType string

const (
	EventStarted    EventType = "started"
	EventIteration  EventType = "iteration"
	EventModelText  EventType = "model_text"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventCompaction EventType = "compaction"
	EventSkill      EventType = "skill_activated"
	EventQuestion   EventType = "question"
	EventAnswer     EventType = "answer"
	EventTodoUpdate EventType = "todo_update"
	EventError      EventType = "error"
	EventDone       EventType = "done"
)

// Reason values for EventDone.
const (
	ReasonFinished = "finished"
	ReasonMaxIter  = "max_iter"
	ReasonCanceled = "canceled"
	ReasonError    = "error"
)

// Event is the structured update emitted by the agent loop. Fields that don't
// apply to a given event type are zero-valued and omitted from JSON.
type Event struct {
	Type      EventType `json:"type"`
	TimeMS    int64     `json:"time_ms"`
	Iter      int       `json:"iter,omitempty"`
	Text      string    `json:"text,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Arguments string    `json:"arguments,omitempty"`
	Result    string    `json:"result,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Summary   string    `json:"summary,omitempty"`

	// Compaction-event fields.
	Kind         string `json:"kind,omitempty"`          // elide | summarize | trim
	TokensBefore int    `json:"tokens_before,omitempty"` // estimated tokens before this step
	TokensAfter  int    `json:"tokens_after,omitempty"`  // estimated tokens after this step

	// Skill-activation fields.
	Skill string `json:"skill,omitempty"` // skill name (for skill_activated events)

	// Question/answer fields. The QuestionID ties together a question and
	// its later answer so the UI knows which input box to clear and the
	// server knows which channel to deliver to.
	QuestionID string   `json:"question_id,omitempty"`
	Question   string   `json:"question,omitempty"`
	Options    []string `json:"options,omitempty"` // optional shortlist of expected answers; UI may render as buttons

	// Todo-update field. Carries the full list emitted by update_todos so the
	// UI can render the plan and the session can persist it.
	Todos []Todo `json:"todos,omitempty"`
}

func newEvent(t EventType) Event {
	return Event{Type: t, TimeMS: time.Now().UnixMilli()}
}
