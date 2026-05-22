package agent

import (
	"testing"

	"github.com/voocel/litellm"
)

func TestEndsWithUserQuestion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{
			"the actual case from the bug report",
			"Before implementation, should I research before every new milestone, only before major or uncertain milestones, or only when I identify a specific risk?",
			true,
		},
		{"plain question at end", "Should I use postgres or sqlite?", true},
		{"empty string", "", false},
		{"whitespace only", "   \n  \t  ", false},
		{"declarative sentence", "I am going to read the file now.", false},
		{"question mark mid-text only", "Is this right? Let me check by running tests.", false},
		{"trailing markdown emphasis", "Should I proceed?*", true},
		{"trailing closing paren/quote", `"Should I proceed?"`, true},
		{"trailing whitespace after question", "Should I proceed?   \n\n", true},
		{"question stem but no ? mark", "Asking the user whether to proceed", false},
		{"multi-paragraph ends with statement", "Let me think.\n\nI'll proceed by reading the file.", false},
		{"multi-paragraph ends with question", "I considered both options.\n\nWhich one do you prefer?", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endsWithUserQuestion(tc.in); got != tc.want {
				t.Errorf("endsWithUserQuestion(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestContainsToolCall(t *testing.T) {
	calls := []litellm.ToolCall{
		{Function: litellm.FunctionCall{Name: "read_file"}},
		{Function: litellm.FunctionCall{Name: "update_todos"}},
	}
	if !containsToolCall(calls, "read_file") {
		t.Error("should find read_file")
	}
	if !containsToolCall(calls, "update_todos") {
		t.Error("should find update_todos")
	}
	if containsToolCall(calls, "ask_user") {
		t.Error("should NOT find ask_user in this set")
	}
	if containsToolCall(nil, "anything") {
		t.Error("nil slice should match nothing")
	}
}
