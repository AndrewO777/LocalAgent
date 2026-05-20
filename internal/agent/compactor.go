package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/litellm"

	"github.com/andrew/localagent/internal/llm"
)

// Compactor shrinks the running conversation between iterations so a long agent
// run doesn't blow past the model's context window. It applies three layers,
// cheapest first:
//
//  1. Elide stale large tool_result contents (deterministic, always on).
//  2. LLM-summarize the older middle of the conversation when above the
//     summarize threshold (requires a compaction LLM).
//  3. Hard-trim oldest turn groups when even after step 2 we're over the hard
//     ceiling — or when there's no compaction LLM to call.
//
// The OpenAI/Anthropic tool-pairing invariant is respected at every layer:
// elision rewrites content but never drops the message; summarize/trim only
// remove whole turn groups (assistant tool_calls + their matching tool
// results).
type Compactor struct {
	// LLM is the model used for step 2 summarization. nil disables step 2
	// (steps 1 and 3 still run).
	LLM *llm.Client

	// ContextTokens is the model's context window. <=0 disables the whole
	// compactor — messages pass through unchanged.
	ContextTokens int

	// Tuning knobs. Sensible defaults are filled in by NewCompactor.
	ElideKeepTurns    int     // turns of recent tool results kept verbatim
	ElideMinBytes     int     // tool results below this aren't elided
	SummarizeKeepTurns int    // turns kept verbatim when summarizing
	SummarizeAt       float64 // fraction of ContextTokens that triggers step 2
	HardTrimAt        float64 // fraction of ContextTokens that forces step 3
	HardTrimTarget    float64 // fraction of ContextTokens to trim down to

	Emit func(Event)
}

// NewCompactor returns a Compactor with sensible defaults. Pass llm=nil to
// disable LLM-based summarization (step 1 + 3 still apply).
func NewCompactor(client *llm.Client, contextTokens int) *Compactor {
	return &Compactor{
		LLM:                client,
		ContextTokens:      contextTokens,
		ElideKeepTurns:     4,
		ElideMinBytes:      2048,
		SummarizeKeepTurns: 6,
		SummarizeAt:        0.75,
		HardTrimAt:         0.95,
		HardTrimTarget:     0.65,
	}
}

// Apply runs the compaction pipeline on messages and returns the possibly
// mutated slice. The input slice is not modified in place.
func (c *Compactor) Apply(ctx context.Context, messages []litellm.Message) []litellm.Message {
	if c == nil || c.ContextTokens <= 0 {
		return messages
	}

	out := append([]litellm.Message(nil), messages...)

	// --- step 1: elide stale tool results -------------------------------
	before := EstimateTokens(out)
	out, elided := elideStaleToolResults(out, c.ElideKeepTurns, c.ElideMinBytes)
	if elided > 0 {
		after := EstimateTokens(out)
		c.emit(elideEvent(elided, before, after))
	}

	// --- step 2: LLM summarize the older middle -------------------------
	tokens := EstimateTokens(out)
	if c.LLM != nil && tokens > int(float64(c.ContextTokens)*c.SummarizeAt) {
		summarized, err := c.summarize(ctx, out)
		if err == nil {
			after := EstimateTokens(summarized)
			c.emit(summarizeEvent(tokens, after, ""))
			out = summarized
			tokens = after
		} else if !errors.Is(err, context.Canceled) {
			c.emit(summarizeEvent(tokens, tokens, "summarization failed: "+err.Error()))
		}
	}

	// --- step 3: hard trim as last resort -------------------------------
	if tokens > int(float64(c.ContextTokens)*c.HardTrimAt) {
		target := int(float64(c.ContextTokens) * c.HardTrimTarget)
		trimmed, dropped := hardTrim(out, target)
		if dropped > 0 {
			after := EstimateTokens(trimmed)
			c.emit(trimEvent(dropped, tokens, after))
			out = trimmed
		}
	}

	return out
}

func (c *Compactor) emit(e Event) {
	if c.Emit != nil {
		c.Emit(e)
	}
}

// --- step 1 ------------------------------------------------------------------

// elideStaleToolResults rewrites the Content of tool messages older than the
// last keepTurns turn-groups, when their content exceeds minBytes. Returns the
// new slice and the number of messages rewritten. Role + ToolCallID are kept
// intact so the assistant -> tool_result pairing remains valid.
func elideStaleToolResults(messages []litellm.Message, keepTurns, minBytes int) ([]litellm.Message, int) {
	if keepTurns <= 0 {
		keepTurns = 1
	}
	// Find the cutoff: index of the keepTurns-th-most-recent assistant turn
	// that has tool_calls. Everything before it is eligible for elision.
	cutoff := 0
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 {
			seen++
			if seen >= keepTurns {
				cutoff = i
				break
			}
		}
	}
	if cutoff == 0 {
		return messages, 0
	}

	// Build an id -> tool name index from assistant messages so we can label
	// the elision stub. We only need to walk up to cutoff.
	names := make(map[string]string)
	for i := 0; i < cutoff; i++ {
		if messages[i].Role != "assistant" {
			continue
		}
		for _, tc := range messages[i].ToolCalls {
			names[tc.ID] = tc.Function.Name
		}
	}

	out := append([]litellm.Message(nil), messages...)
	rewrites := 0
	for i := 0; i < cutoff; i++ {
		if out[i].Role != "tool" {
			continue
		}
		if len(out[i].Content) < minBytes {
			continue
		}
		// Idempotent: a previously elided message starts with our stub.
		if strings.HasPrefix(out[i].Content, "[elided:") {
			continue
		}
		name := names[out[i].ToolCallID]
		if name == "" {
			name = "tool"
		}
		out[i].Content = fmt.Sprintf("[elided: %s output, %d bytes — re-run if you need it again]", name, len(out[i].Content))
		rewrites++
	}
	return out, rewrites
}

// --- step 2 ------------------------------------------------------------------

const summarizerSystem = `You are a context compactor for a coding agent. You receive part of the agent's prior conversation and produce a tight summary that preserves only what future iterations need to keep working.

Cover, in this order, with terse bullet fragments — no preamble, no closing remarks:
- Goal: the user's original objective + any constraints.
- Files touched: path and a half-line of what changed.
- Commands run: command and outcome (ok / failed: <reason>).
- Decisions made: any non-obvious choice the agent committed to.
- Open TODOs / unresolved issues / pending checks.
- Errors that recurred or remain.

Do not invent. Do not add suggestions. If a section has nothing, omit it.`

// summarize replaces the older middle of the conversation with a single user
// message containing an LLM-generated summary. Pinned messages (system +
// initial user goal) and the last SummarizeKeepTurns turn groups pass through
// unchanged. Returns an error if the LLM call fails.
func (c *Compactor) summarize(ctx context.Context, messages []litellm.Message) ([]litellm.Message, error) {
	pinned, middle, recent := splitForSummary(messages, c.SummarizeKeepTurns)
	if len(middle) == 0 {
		return messages, nil // nothing in the middle to compress
	}

	summary, err := c.callSummarizer(ctx, middle)
	if err != nil {
		return nil, err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil, errors.New("summarizer returned empty output")
	}

	out := make([]litellm.Message, 0, len(pinned)+1+len(recent))
	out = append(out, pinned...)
	out = append(out, litellm.UserMessage("[Earlier conversation summarized to save context]\n"+summary))
	out = append(out, recent...)
	return out, nil
}

// splitForSummary divides messages into three contiguous regions:
//   - pinned: system message + initial user goal (always preserved verbatim).
//   - middle: candidates for summarization.
//   - recent: the last keepTurns turn groups, plus any trailing user message
//     (e.g. a /continue instruction).
//
// A "turn group" is an assistant message and all tool messages that follow
// before the next user/assistant. Turn-group boundaries are anchored on
// assistant messages — we count keepTurns of them from the end.
func splitForSummary(messages []litellm.Message, keepTurns int) (pinned, middle, recent []litellm.Message) {
	if keepTurns <= 0 {
		keepTurns = 1
	}

	// Pinned: system (if present at [0]) + first user message.
	pinnedEnd := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		pinnedEnd = 1
	}
	if pinnedEnd < len(messages) && messages[pinnedEnd].Role == "user" {
		pinnedEnd++
	}

	// Find where "recent" starts: scan backwards, find the keepTurns-th
	// assistant message. Recent begins at that assistant index. If there's a
	// user message immediately before it (the prompt that produced it), pull
	// it in too — the assistant turn doesn't make sense without its prompt.
	recentStart := len(messages)
	seen := 0
	for i := len(messages) - 1; i >= pinnedEnd; i-- {
		if messages[i].Role == "assistant" {
			seen++
			if seen >= keepTurns {
				recentStart = i
				if i-1 >= pinnedEnd && messages[i-1].Role == "user" {
					recentStart = i - 1
				}
				break
			}
		}
	}
	if recentStart < pinnedEnd {
		recentStart = pinnedEnd
	}

	pinned = messages[:pinnedEnd]
	middle = messages[pinnedEnd:recentStart]
	recent = messages[recentStart:]
	return
}

// callSummarizer formats the middle slice as plain text and asks the compaction
// LLM to summarize it. Streamed output is accumulated into a single string.
func (c *Compactor) callSummarizer(ctx context.Context, middle []litellm.Message) (string, error) {
	var b strings.Builder
	for _, m := range middle {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "USER: %s\n", oneline(m.Content))
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", oneline(m.Content))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "TOOL_CALL: %s(%s)\n", tc.Function.Name, oneline(tc.Function.Arguments))
			}
		case "tool":
			fmt.Fprintf(&b, "TOOL_RESULT: %s\n", oneline(m.Content))
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	temp := 0.1
	stream, err := c.LLM.Stream(ctx, &litellm.Request{
		Model: c.LLM.Model,
		Messages: []litellm.Message{
			litellm.SystemMessage(summarizerSystem),
			litellm.UserMessage("Conversation to compact:\n\n" + b.String()),
		},
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("compaction stream open: %w", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		chunk, err := stream.Next()
		if err != nil {
			if errors.Is(err, litellm.ErrStreamIdle) {
				return "", errors.New("compaction LLM stalled")
			}
			return "", err
		}
		if chunk == nil {
			break
		}
		content.WriteString(chunk.Content)
		if chunk.Done {
			break
		}
	}
	return content.String(), nil
}

// oneline collapses internal newlines so the formatted summary input doesn't
// look like its own transcript inside the prompt.
func oneline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) > 4000 {
		s = s[:4000] + " […truncated]"
	}
	return s
}

// --- step 3 ------------------------------------------------------------------

// hardTrim drops oldest turn-groups (assistant + its tool results, optionally
// preceded by a user message) until estimated tokens fall under target. The
// first two messages (system + initial user goal) are always preserved.
// Returns the trimmed slice and the number of messages dropped.
func hardTrim(messages []litellm.Message, target int) ([]litellm.Message, int) {
	pinnedEnd := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		pinnedEnd = 1
	}
	if pinnedEnd < len(messages) && messages[pinnedEnd].Role == "user" {
		pinnedEnd++
	}

	out := append([]litellm.Message(nil), messages...)
	dropped := 0
	for EstimateTokens(out) > target {
		// Find the next turn-group start after the pinned prefix.
		groupStart := pinnedEnd
		if groupStart >= len(out) {
			break
		}
		// Advance over a leading user message (a /continue prompt) if present
		// before the assistant turn.
		groupEnd := groupStart
		if groupEnd < len(out) && out[groupEnd].Role == "user" {
			groupEnd++
		}
		if groupEnd < len(out) && out[groupEnd].Role == "assistant" {
			groupEnd++
		} else {
			// Malformed sequence — stop trimming rather than risk an orphan.
			break
		}
		// Consume contiguous tool messages.
		for groupEnd < len(out) && out[groupEnd].Role == "tool" {
			groupEnd++
		}
		if groupEnd == groupStart {
			break
		}
		// Don't trim everything — leave at least one recent turn.
		if groupEnd >= len(out) {
			break
		}
		dropped += groupEnd - groupStart
		out = append(out[:groupStart], out[groupEnd:]...)
	}
	return out, dropped
}

// --- token estimation -------------------------------------------------------

// EstimateTokens returns a rough token count for the message slice. Uses the
// standard ~4 chars/token approximation plus a small per-message overhead.
// Good enough for trigger decisions; not for billing.
func EstimateTokens(messages []litellm.Message) int {
	const perMessageOverhead = 4 // role + framing tokens
	total := 0
	for _, m := range messages {
		total += perMessageOverhead
		total += len(m.Role) / 4
		total += len(m.Content) / 4
		total += len(m.ToolCallID) / 4
		for _, tc := range m.ToolCalls {
			total += 2
			total += len(tc.Function.Name) / 4
			total += len(tc.Function.Arguments) / 4
		}
	}
	return total
}

// --- event helpers ----------------------------------------------------------

func elideEvent(count, before, after int) Event {
	e := newEvent(EventCompaction)
	e.Kind = "elide"
	e.TokensBefore = before
	e.TokensAfter = after
	e.Text = fmt.Sprintf("elided %d stale tool result(s)", count)
	return e
}

func summarizeEvent(before, after int, errText string) Event {
	e := newEvent(EventCompaction)
	e.Kind = "summarize"
	e.TokensBefore = before
	e.TokensAfter = after
	if errText != "" {
		e.Text = errText
		e.IsError = true
	} else {
		e.Text = "summarized older conversation"
	}
	return e
}

func trimEvent(dropped, before, after int) Event {
	e := newEvent(EventCompaction)
	e.Kind = "trim"
	e.TokensBefore = before
	e.TokensAfter = after
	e.Text = fmt.Sprintf("hard-trimmed %d message(s) to fit context", dropped)
	return e
}
