package agent

import (
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

// buildConversation returns a synthetic conversation with N turn groups, each
// having one tool call whose result is a `size`-byte blob. Layout:
//
//	[system, user(goal), (assistant+tool)*N]
func buildConversation(turns, size int) []litellm.Message {
	msgs := []litellm.Message{
		litellm.SystemMessage("you are an agent"),
		litellm.UserMessage("Goal: do the thing"),
	}
	blob := strings.Repeat("x", size)
	for i := 0; i < turns; i++ {
		id := "call_" + string(rune('a'+i))
		msgs = append(msgs, litellm.Message{
			Role: "assistant",
			ToolCalls: []litellm.ToolCall{{
				ID:       id,
				Type:     "function",
				Function: litellm.FunctionCall{Name: "read_file", Arguments: `{"path":"x"}`},
			}},
		})
		msgs = append(msgs, litellm.ToolMessage(id, blob))
	}
	return msgs
}

// assertToolPairing fails the test if any assistant tool_call lacks a matching
// tool message immediately after, or any tool message lacks a preceding
// assistant tool_call.
func assertToolPairing(t *testing.T, msgs []litellm.Message) {
	t.Helper()
	pending := map[string]bool{}
	for i, m := range msgs {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
			}
		case "tool":
			if !pending[m.ToolCallID] {
				t.Fatalf("msg %d: tool message with id %q has no matching assistant tool_call", i, m.ToolCallID)
			}
			delete(pending, m.ToolCallID)
		}
	}
	if len(pending) != 0 {
		t.Fatalf("orphan tool_calls without matching tool result: %v", pending)
	}
}

func TestElideStaleToolResults_PreservesRecentAndPairing(t *testing.T) {
	msgs := buildConversation(10, 5000) // 10 turns, ~5KB each
	out, count := elideStaleToolResults(msgs, 3, 2048)

	if count == 0 {
		t.Fatal("expected some elisions, got 0")
	}
	if got := len(out); got != len(msgs) {
		t.Fatalf("message count must not change: was %d, got %d", len(msgs), got)
	}

	// The last 3 assistant turns + their tool results must be untouched.
	// Walk back; check the trailing 6 messages (3 assistant + 3 tool) are unchanged.
	for i := len(out) - 6; i < len(out); i++ {
		if out[i].Content != msgs[i].Content {
			t.Fatalf("msg %d in recent window was modified: %q -> %q", i, msgs[i].Content, out[i].Content)
		}
	}
	// Older tool results should have been elided to the stub format.
	for i := 2; i < len(out)-6; i++ {
		if out[i].Role != "tool" {
			continue
		}
		if !strings.HasPrefix(out[i].Content, "[elided:") {
			t.Fatalf("msg %d: older tool result not elided: %q", i, out[i].Content)
		}
		if !strings.Contains(out[i].Content, "read_file") {
			t.Fatalf("msg %d: elision stub lost tool name: %q", i, out[i].Content)
		}
	}
	assertToolPairing(t, out)
}

func TestElideStaleToolResults_Idempotent(t *testing.T) {
	msgs := buildConversation(8, 5000)
	first, _ := elideStaleToolResults(msgs, 3, 2048)
	second, count := elideStaleToolResults(first, 3, 2048)
	if count != 0 {
		t.Fatalf("second pass should re-elide nothing, got count=%d", count)
	}
	for i := range first {
		if first[i].Content != second[i].Content {
			t.Fatalf("msg %d changed on second pass: %q -> %q", i, first[i].Content, second[i].Content)
		}
	}
}

func TestElideStaleToolResults_SkipsSmallResults(t *testing.T) {
	msgs := buildConversation(8, 100) // tiny results
	_, count := elideStaleToolResults(msgs, 3, 2048)
	if count != 0 {
		t.Fatalf("small results should not be elided, got count=%d", count)
	}
}

func TestSplitForSummary_PreservesPinnedAndRecent(t *testing.T) {
	msgs := buildConversation(10, 1000)
	pinned, middle, recent := splitForSummary(msgs, 3)

	if len(pinned) != 2 {
		t.Fatalf("pinned should be [system, user(goal)], got len=%d", len(pinned))
	}
	if pinned[0].Role != "system" || pinned[1].Role != "user" {
		t.Fatalf("pinned roles wrong: %q %q", pinned[0].Role, pinned[1].Role)
	}
	if len(middle)+len(recent)+len(pinned) != len(msgs) {
		t.Fatalf("partition lost messages: %d+%d+%d != %d", len(pinned), len(middle), len(recent), len(msgs))
	}
	// Recent should start at an assistant message and include exactly the
	// last 3 turn groups (6 messages: 3 assistant + 3 tool).
	if len(recent) != 6 {
		t.Fatalf("recent should be 6 messages (3 turns), got %d", len(recent))
	}
	if recent[0].Role != "assistant" {
		t.Fatalf("recent should start at an assistant turn, got %q", recent[0].Role)
	}
	// All three regions concatenated should equal the input.
	combined := append(append([]litellm.Message{}, pinned...), middle...)
	combined = append(combined, recent...)
	for i := range msgs {
		if combined[i].Content != msgs[i].Content || combined[i].Role != msgs[i].Role {
			t.Fatalf("msg %d differs after split+rejoin", i)
		}
	}
}

func TestHardTrim_DropsOldestTurnGroups(t *testing.T) {
	msgs := buildConversation(20, 4000)
	before := EstimateTokens(msgs)
	target := before / 3 // force aggressive trimming
	out, dropped := hardTrim(msgs, target)

	if dropped == 0 {
		t.Fatal("expected trimming, got none")
	}
	// System + initial user goal must survive.
	if out[0].Role != "system" {
		t.Fatalf("system message dropped")
	}
	if out[1].Role != "user" || !strings.HasPrefix(out[1].Content, "Goal:") {
		t.Fatalf("initial user goal dropped")
	}
	// Pairing must still hold.
	assertToolPairing(t, out)
	// Should be at or below the target (within one turn-group of it).
	if got := EstimateTokens(out); got > target+2000 {
		t.Fatalf("trim didn't reach target: target=%d got=%d", target, got)
	}
}

func TestEstimateTokens_GrowsWithContent(t *testing.T) {
	small := buildConversation(2, 100)
	big := buildConversation(2, 10000)
	if EstimateTokens(big) <= EstimateTokens(small) {
		t.Fatal("estimate should grow with content size")
	}
}
