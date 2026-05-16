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
}

func newEvent(t EventType) Event {
	return Event{Type: t, TimeMS: time.Now().UnixMilli()}
}
