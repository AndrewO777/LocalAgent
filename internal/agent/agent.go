package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/prompts"
	"github.com/voocel/litellm"

	"github.com/andrew/localagent/internal/llm"
	"github.com/andrew/localagent/internal/tools"
)

// llmCallTimeout bounds a single Stream() round-trip as a backstop. The
// real stall detector is litellm's StreamIdleTimeout (60s); this is here so
// even a pathologically slow-but-not-quite-idle stream eventually returns.
const llmCallTimeout = 10 * time.Minute

// systemPromptTmpl is filled in via a langchaingo PromptTemplate so the
// workdir and iteration budget are injected at runtime.
const systemPromptTmpl = `You are an autonomous software engineering agent. You operate inside the project directory "{{.workdir}}" and accomplish the user's goal by repeatedly calling tools.

You have at most {{.max_iter}} iterations to complete the task.

Working style:
- Start by listing the project root with list_dir to understand the layout.
- Read existing files before modifying them.
- Make small, verifiable changes. After writing code, run commands (build, test, lint) to confirm it works.
- If a command fails, read the error carefully, fix the root cause, and retry. Do not give up after one failure.
- Prefer edit_file for targeted changes; use write_file for new files or full rewrites.
- When the user's goal is complete and verified, call finish with a short summary.

Constraints:
- All paths are relative to the project root. Absolute paths and paths escaping the root are rejected.
- Each tool call should make progress. Do not call the same tool with the same arguments twice in a row.
- run_command waits for the command to exit. Do NOT start dev servers or watch processes (npm run dev, npx vite, anything with --watch) — they will hit the timeout and be killed. To verify code runs, use one-shot commands that exit on their own: npm test, npm run build, go build ./..., go test ./..., etc.`

// Config controls one agent run.
type Config struct {
	LLM           *llm.Client
	Tools         *tools.Registry
	Goal          string
	MaxIterations int

	// InitialMessages, if non-empty, seeds the conversation with prior history
	// (used by /api/sessions/{id}/continue). When set, Goal is appended as a
	// follow-up user message instead of starting a fresh conversation.
	InitialMessages []litellm.Message

	// Emit is invoked synchronously for every event. Must not block for long;
	// the server adapter funnels into a buffered channel.
	Emit func(Event)

	// OnMessages, if set, is invoked after each iteration with a snapshot of
	// the full message history. The server uses this to persist conversation
	// state for the continue feature.
	OnMessages func([]litellm.Message)
}

// Run drives the agent loop until finish is called, MaxIterations is reached,
// or the context is canceled. It always emits a terminal EventDone before
// returning.
func Run(ctx context.Context, cfg Config) (err error) {
	if cfg.Emit == nil {
		cfg.Emit = func(Event) {}
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 25
	}

	emit := cfg.Emit
	defer func() {
		if err == nil {
			return
		}
		ev := newEvent(EventDone)
		switch {
		case errors.Is(err, context.Canceled):
			ev.Reason = ReasonCanceled
		case strings.Contains(err.Error(), "max iterations"):
			ev.Reason = ReasonMaxIter
		default:
			ev.Reason = ReasonError
			ev.Text = err.Error()
		}
		emit(ev)
	}()

	// Convert provider-agnostic tool definitions to litellm.Tool.
	var ltools []litellm.Tool
	for _, t := range cfg.Tools.List() {
		ltools = append(ltools, litellm.NewTool(t.Name, t.Description, t.Parameters))
	}

	// Build the starting message list. Continue mode reuses the prior history.
	var messages []litellm.Message
	if len(cfg.InitialMessages) > 0 {
		messages = append([]litellm.Message(nil), cfg.InitialMessages...)
		messages = append(messages, litellm.UserMessage(fmt.Sprintf(
			"Continuing the previous session. You have up to %d more iterations. New instruction: %s",
			cfg.MaxIterations, cfg.Goal,
		)))
	} else {
		tmpl := prompts.NewPromptTemplate(systemPromptTmpl, []string{"workdir", "max_iter"})
		sysPrompt, perr := tmpl.Format(map[string]any{
			"workdir":  cfg.Tools.Workdir(),
			"max_iter": cfg.MaxIterations,
		})
		if perr != nil {
			return fmt.Errorf("prompt template: %w", perr)
		}
		messages = []litellm.Message{
			litellm.SystemMessage(sysPrompt),
			litellm.UserMessage("Goal: " + cfg.Goal),
		}
	}

	startEv := newEvent(EventStarted)
	startEv.Text = cfg.Goal
	emit(startEv)

	snapshot := func() {
		if cfg.OnMessages != nil {
			cfg.OnMessages(append([]litellm.Message(nil), messages...))
		}
	}
	snapshot() // capture the seed state

	temp := 0.2
	for iter := 1; iter <= cfg.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		iterEv := newEvent(EventIteration)
		iterEv.Iter = iter
		emit(iterEv)

		content, toolCalls, streamErr := streamOne(ctx, cfg.LLM, &litellm.Request{
			Model:       cfg.LLM.Model,
			Messages:    messages,
			Tools:       ltools,
			ToolChoice:  "auto",
			Temperature: &temp,
		})
		if streamErr != nil {
			return streamErr
		}

		if text := strings.TrimSpace(content); text != "" {
			textEv := newEvent(EventModelText)
			textEv.Text = text
			emit(textEv)
		}

		// No tool calls: nudge the model once. If it persists, the iteration
		// budget will run out — preferable to looping forever silently.
		if len(toolCalls) == 0 {
			messages = append(messages,
				litellm.Message{Role: "assistant", Content: content},
				litellm.UserMessage("You must either call a tool to make progress, or call finish if the goal is complete."),
			)
			snapshot()
			continue
		}

		// Append the assistant turn with tool calls.
		messages = append(messages, litellm.Message{
			Role:      "assistant",
			Content:   content,
			ToolCalls: toolCalls,
		})

		// Execute every tool call the model issued this turn.
		for _, tc := range toolCalls {
			callEv := newEvent(EventToolCall)
			callEv.Tool = tc.Function.Name
			callEv.Arguments = tc.Function.Arguments
			emit(callEv)

			result, callErr := cfg.Tools.Call(ctx, tc.Function.Name, tc.Function.Arguments)
			if errors.Is(callErr, tools.ErrFinished) {
				messages = append(messages, litellm.ToolMessage(tc.ID, result))
				snapshot()
				doneEv := newEvent(EventDone)
				doneEv.Reason = ReasonFinished
				doneEv.Summary = result
				emit(doneEv)
				return nil
			}
			isErr := callErr != nil
			if isErr {
				result = "ERROR: " + callErr.Error()
			}
			resEv := newEvent(EventToolResult)
			resEv.Tool = tc.Function.Name
			resEv.Result = result
			resEv.IsError = isErr
			emit(resEv)

			messages = append(messages, litellm.ToolMessage(tc.ID, result))
		}
		snapshot()
	}
	return fmt.Errorf("reached max iterations (%d) without finishing", cfg.MaxIterations)
}

// streamOne issues a single Stream call and accumulates content + tool calls
// until the terminal chunk. Idle stalls are surfaced as a clear error.
func streamOne(parentCtx context.Context, c *llm.Client, req *litellm.Request) (string, []litellm.ToolCall, error) {
	ctx, cancel := context.WithTimeout(parentCtx, llmCallTimeout)
	defer cancel()

	stream, err := c.Stream(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("llm stream open: %w", err)
	}
	defer stream.Close()

	acc := litellm.NewToolCallAccumulator()
	var content strings.Builder
	for {
		chunk, err := stream.Next()
		if err != nil {
			if errors.Is(err, litellm.ErrStreamIdle) {
				return "", nil, fmt.Errorf("ollama stalled: no chunks in 60s (model loaded but not generating — try restarting Ollama)")
			}
			if errors.Is(err, context.Canceled) || errors.Is(parentCtx.Err(), context.Canceled) {
				return "", nil, context.Canceled
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return "", nil, fmt.Errorf("llm call timed out after %s", llmCallTimeout)
			}
			return "", nil, fmt.Errorf("llm stream read: %w", err)
		}
		if chunk == nil {
			break
		}
		if chunk.Content != "" {
			content.WriteString(chunk.Content)
		}
		if chunk.ToolCallDelta != nil {
			acc.Apply(chunk.ToolCallDelta)
		}
		if chunk.Done {
			break
		}
	}
	return content.String(), acc.Build(), nil
}
