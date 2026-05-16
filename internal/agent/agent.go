package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/prompts"
	"github.com/voocel/litellm"

	"github.com/andrew/localagent/internal/llm"
	"github.com/andrew/localagent/internal/tools"
)

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
- Each tool call should make progress. Do not call the same tool with the same arguments twice in a row.`

// Config controls one agent run.
type Config struct {
	LLM           *llm.Client
	Tools         *tools.Registry
	Goal          string
	MaxIterations int
	// Emit is invoked synchronously for every event. Must not block for long;
	// the server adapter funnels into a buffered channel.
	Emit func(Event)
}

// Run drives the agent loop until finish is called, MaxIterations is reached,
// or the context is canceled. It always emits a terminal EventDone before
// returning (unless the context is canceled mid-call).
func Run(ctx context.Context, cfg Config) (err error) {
	if cfg.Emit == nil {
		cfg.Emit = func(Event) {}
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 25
	}

	emit := cfg.Emit
	defer func() {
		// Translate the final state into a done event. If the caller already
		// emitted one via finish/cancel, this becomes a no-op for the UI as it
		// just shows the last reason.
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

	tmpl := prompts.NewPromptTemplate(systemPromptTmpl, []string{"workdir", "max_iter"})
	sysPrompt, perr := tmpl.Format(map[string]any{
		"workdir":  cfg.Tools.Workdir(),
		"max_iter": cfg.MaxIterations,
	})
	if perr != nil {
		return fmt.Errorf("prompt template: %w", perr)
	}

	// Convert provider-agnostic tool definitions to litellm.Tool.
	var ltools []litellm.Tool
	for _, t := range cfg.Tools.List() {
		ltools = append(ltools, litellm.NewTool(t.Name, t.Description, t.Parameters))
	}

	messages := []litellm.Message{
		litellm.SystemMessage(sysPrompt),
		litellm.UserMessage("Goal: " + cfg.Goal),
	}

	emit(Event{Type: EventStarted, TimeMS: newEvent(EventStarted).TimeMS, Text: cfg.Goal})

	temp := 0.2
	for iter := 1; iter <= cfg.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		ev := newEvent(EventIteration)
		ev.Iter = iter
		emit(ev)

		resp, err := cfg.LLM.Chat(ctx, &litellm.Request{
			Model:       cfg.LLM.Model,
			Messages:    messages,
			Tools:       ltools,
			ToolChoice:  "auto",
			Temperature: &temp,
		})
		if err != nil {
			return fmt.Errorf("llm call failed: %w", err)
		}

		if text := strings.TrimSpace(resp.Content); text != "" {
			ev := newEvent(EventModelText)
			ev.Text = text
			emit(ev)
		}

		// No tool calls: nudge the model once. If it persists, the iteration
		// budget will run out — preferable to looping forever silently.
		if len(resp.ToolCalls) == 0 {
			messages = append(messages,
				litellm.Message{Role: "assistant", Content: resp.Content},
				litellm.UserMessage("You must either call a tool to make progress, or call finish if the goal is complete."),
			)
			continue
		}

		// Append the assistant turn with tool calls.
		messages = append(messages, litellm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute every tool call the model issued this turn.
		for _, tc := range resp.ToolCalls {
			callEv := newEvent(EventToolCall)
			callEv.Tool = tc.Function.Name
			callEv.Arguments = tc.Function.Arguments
			emit(callEv)

			result, callErr := cfg.Tools.Call(ctx, tc.Function.Name, tc.Function.Arguments)
			if errors.Is(callErr, tools.ErrFinished) {
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
	}
	return fmt.Errorf("reached max iterations (%d) without finishing", cfg.MaxIterations)
}
