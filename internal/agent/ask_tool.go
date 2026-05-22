package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/andrew/localagent/internal/tools"
)

// AskUserFunc is the callback the server implements to hand off a question
// from the agent loop to the human via the UI. Implementations are expected
// to:
//   1. Generate or accept the questionID and register a delivery channel.
//   2. Emit a `question` event on the session so the UI sees it.
//   3. Block until either an answer arrives or ctx is canceled.
//   4. On answer, emit an `answer` event and return it.
//
// The closure pattern keeps the agent package free of any dependency on the
// session/store layers — agent.Config just receives a function.
type AskUserFunc func(ctx context.Context, questionID, question string, options []string) (string, error)

// newAskUserTool builds the ask_user tool, parameterised by the AskUser
// callback. The returned tool blocks the agent loop until the user answers
// (or the run is canceled).
func newAskUserTool(ask AskUserFunc) tools.Tool {
	return tools.Tool{
		Name: "ask_user",
		Description: "Ask the human a question and wait for their response. " +
			"Use when you need clarification, approval to proceed, or a decision " +
			"that meaningfully affects how you continue. Examples: " +
			"'Should I use sqlite or postgres?', 'Ready to proceed with milestone 2?', " +
			"'I'm about to delete src/legacy/ — confirm?'. " +
			"Do NOT use ask_user for things you can decide yourself or trivially try; " +
			"running this tool blocks the entire agent loop until the user types a " +
			"reply, so use it sparingly and only when the answer changes what you'll " +
			"do next. The user's answer is returned as the tool result.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask. Be specific and concise. State what's blocking you and what answer you need.",
				},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional shortlist of expected answers (e.g. [\"postgres\", \"sqlite\"]). The UI may render these as quick-pick buttons. Users can still answer freeform.",
				},
			},
			"required": []string{"question"},
		},
		Handler: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Question string   `json:"question"`
				Options  []string `json:"options,omitempty"`
			}
			dec := json.NewDecoder(strings.NewReader(args))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(in.Question) == "" {
				return "", errors.New("question is required")
			}
			id := newQuestionID()
			ans, err := ask(ctx, id, in.Question, in.Options)
			if err != nil {
				return "", err
			}
			return ans, nil
		},
	}
}

// newQuestionID returns a short hex token that's unique-enough for a single
// session's lifetime.
func newQuestionID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "q_" + hex.EncodeToString(b[:])
}
