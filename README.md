# LocalAgent

An agentic coding loop in Go. Drives a local LLM through [Ollama](https://ollama.com)
to read, write, and run code inside a project directory. Ships with a CLI mode
and an HTTP server that serves a single-page React UI for live tailing.

- **LLM gateway:** [voocel/litellm](https://github.com/voocel/litellm) — unified
  Go client (Ollama via its OpenAI-compatible endpoint; swappable to OpenAI /
  Anthropic / etc. by changing one constructor).
- **Prompt templating:** [tmc/langchaingo](https://github.com/tmc/langchaingo).
- **UI:** React 18 + Babel standalone, served from a single embedded
  `index.html` — no `npm install` step.

## Prerequisites

- **Go 1.23+** (1.25 used during development).
- **Ollama** installed and running locally: <https://ollama.com/download>.
- **A model that supports tool calling.** Examples that work:
  - `qwen2.5-coder:7b` (recommended for code tasks)
  - `llama3.1`
  - `mistral-nemo`

  Pull one before first run:

  ```
  ollama pull qwen2.5-coder:7b
  ```

## Build

```
git clone <this repo>
cd LocalAgent
go mod download
go build -o LocalAgent.exe .       # Windows
go build -o LocalAgent .           # macOS / Linux
```

The web UI is embedded in the binary at build time via `//go:embed`, so the
single executable is fully self-contained.

## Run — server + web UI

```
./LocalAgent -serve -addr :8080
```

Then open <http://localhost:8080> and fill in the form:

| Field | Default | Notes |
|---|---|---|
| Model | `qwen2.5-coder:7b` | Any Ollama model id that supports tool calling. |
| Ollama host | `http://localhost:11434` | Override if Ollama runs elsewhere. |
| Project directory | `.` | Absolute or relative. **All file/shell ops are sandboxed here.** |
| Compaction model | *(empty)* | Optional. Smaller model used for context compaction. Empty = reuse main model. See **Context window** below. |
| Goal | *(required)* | Natural-language task for the agent. |
| Context window | `32768` | Token budget the compactor protects. Set to `0` to disable compaction entirely. See **Context window** below. |
| Skills | *(none)* | Checkboxes for discovered skills. Also activatable via `/skill-name` prefix in the goal text. See **Skills** below. |

The agent works from a **todo list it generates itself**, not a fixed
iteration budget. The first action of every run is `update_todos` —
3-8 concrete milestones — and the run completes when the agent calls
`finish`. There's a hidden safety cap (200 iterations) as a backstop;
you'll only ever see it in pathological loops.

Click **Run agent**. Iteration headers, model output, tool calls, and tool
results stream in via Server-Sent Events. **Cancel run** aborts mid-loop.

## Context window

Long agent runs grow their conversation past most models' context windows.
LocalAgent runs a three-stage compactor between iterations:

1. **Elide stale tool results** — replaces old large `read_file` / `run_command`
   outputs with `[elided: <tool> output, <N> bytes]`. Cheap, deterministic,
   always on. Last 4 turns are kept verbatim.
2. **LLM-summarize the older middle** above 75% of the configured budget,
   using the **Compaction model** (or the main model if you leave the field
   blank). Pinned: system prompt + initial goal + last 6 turns.
3. **Hard-trim oldest turn groups** above 95% as a last resort.

You'll see `⌬ elide / summarize / trim` events in the timeline whenever the
compactor acts, with before/after token estimates.

### Recommended `Context window` values

These are budgets the *compactor* targets — set the value to match what your
GPU + Ollama configuration can actually serve (see the gotcha below). The
~4 char/token estimate the compactor uses is rough, so leave ~10-15% headroom.

| GPU VRAM | 7B model (Q4_K_M) | 14B model | 32B model |
|---|---|---|---|
| 8 GB | 8k–16k | — | — |
| 12 GB | 32k | 8k | — |
| 16 GB | 64k–96k | 16k–32k | — |
| **24 GB (e.g. 7900 XTX / 3090 / 4090)** | **65k–128k (native max)** | 32k–64k | ~16k |
| 48 GB | 128k+ | 96k+ | 32k–64k |

Set **Context window** to `0` to disable compaction entirely — useful for
short runs where the agent will finish in <10 iterations.

### Ollama `num_ctx` gotcha — and how to fix it

**Ollama does *not* automatically use a model's native max context.** Every
stock model is served with `num_ctx=4096` (or whatever `OLLAMA_CONTEXT_LENGTH`
is set to) unless you override it. Setting **Context window** in LocalAgent's
UI does *not* change this — if Ollama is serving 4096 tokens, prompts longer
than that are silently truncated and the model loses context.

**LocalAgent detects this automatically.** At the start of every run it
probes Ollama's `/api/ps` and `/api/show` endpoints. If the effective
`num_ctx` is smaller than your UI setting, a `⚠` warning appears in the
event stream and the compactor's budget is clamped to what Ollama will
actually serve. You don't lose data, but you also won't get the headroom
you configured until you fix Ollama.

**Three ways to fix it, in order of preference:**

1. **Per-model Modelfile** (recommended — explicit, per-model, survives
   restarts):

   ```
   # Modelfile
   FROM qwen2.5-coder:7b
   PARAMETER num_ctx 65536
   ```

   Then:

   ```
   ollama create qwen2.5-coder:7b-65k -f ./Modelfile
   ```

   …and use `qwen2.5-coder:7b-65k` as the model id in LocalAgent.

2. **Server-wide env var** (affects every model Ollama loads):

   ```
   # Linux/macOS
   OLLAMA_CONTEXT_LENGTH=65536 ollama serve

   # Windows PowerShell
   $env:OLLAMA_CONTEXT_LENGTH = 65536; ollama serve
   ```

3. **Per-request override** — *not currently possible* through Ollama's
   OpenAI-compatible endpoint (which is what LocalAgent uses via litellm).
   Use option 1 or 2.

After changing num_ctx, confirm with:

```
curl http://localhost:11434/api/ps
```

The `context_length` field for your loaded model should match what you set.

## Run — CLI mode

```
./LocalAgent -workdir ./myproject -goal "Add a /healthz endpoint and a test"
```

Other flags:

```
-model     Ollama model id                                (default qwen2.5-coder:7b)
-host      Ollama base URL                                (default http://localhost:11434)
-max-iter  hidden safety cap on iterations (rarely hit)   (default 200)
-goal      task; reads stdin if empty
```

Output goes to stderr with the same event types as the UI.

## What the agent can do

The model gets six tools, all sandboxed to the workdir:

| Tool | Purpose |
|---|---|
| `list_dir(path)` | List a directory. |
| `read_file(path)` | Read a UTF-8 file (capped at 200 KB). |
| `write_file(path, content)` | Create or overwrite a file. |
| `edit_file(path, old_text, new_text)` | Unique-match string replace. |
| `run_command(command, timeout_sec?)` | Run a shell command. PowerShell on Windows, `/bin/sh -c` elsewhere. 120 s default timeout (max 600 s), 32 KB output cap, cwd locked to workdir. Spawned in its own process group; on timeout/cancel the entire **process tree** is killed (`taskkill /F /T` on Windows, `kill -- -pgid` on Unix). Not suitable for long-running dev servers — the model is told to avoid them. |
| `ask_user(question, options?)` | Pause the loop and ask the human for input. Only registered when running under the server (the UI provides the answer box). Blocks until the user types a reply or the run is canceled. Needed for human-in-the-loop skills like WNP-Loop. |
| `update_todos(todos)` | Set or revise the agent's plan. Each call replaces the whole list. Each item has `content` + `status` (`pending` / `in_progress` / `completed`). Called automatically at the start of every run and again after each milestone. |
| `finish(summary)` | Signal task complete. Should only be called when every todo is completed. |

Sandboxing rules: absolute paths and `..` escapes are rejected at the tool
boundary.

## Skills

A **skill** is a modular instruction bundle that changes the agent's
behaviour — make outputs terse (caveman), follow a research-plan-proceed
workflow (WNP-Loop), enforce TDD, switch to a project-specific code style,
etc. LocalAgent uses the Anthropic Claude Code `SKILL.md` format unchanged,
so skills written for Claude Code, Codex CLI, OpenCode, and Cursor drop in
without modification.

### Format

A skill is a directory containing one `SKILL.md` file:

```
my-skill/
└── SKILL.md
```

`SKILL.md` starts with YAML frontmatter, then a markdown body:

```markdown
---
name: my-skill
description: One-paragraph summary the agent uses to decide when to load this skill.
---

# Instructions

Whatever you want the agent to do. Plain markdown.
```

Names must be lowercase-kebab-case (`^[a-z][a-z0-9-]{0,63}$`) — they appear
in slash commands and tool arguments.

### Where to put them

Two locations are scanned at session start; project beats user on name
collision:

| Scope | Path |
|---|---|
| User-global | `~/.localagent/skills/<name>/SKILL.md` |
| Project-local | `<workdir>/.localagent/skills/<name>/SKILL.md` |

### Three ways to activate

1. **Checkbox in the UI** — discovered skills show up as checkboxes in the
   sidebar. Pick before running. The selection persists across sessions in
   `localStorage`.
2. **Slash command in the goal** — start your goal with `/skill-name`:

   ```
   /wnp-loop /caveman Build a /healthz endpoint and a test
   ```

   Both skills activate; the prefix gets stripped before the model sees
   the goal. Up to 8 stacked commands honored. Unknown commands are left
   in place (so typos don't silently break anything).
3. **The model decides** — if any unselected skills exist in the catalog,
   the agent gets an `activate_skill(name)` tool. The system prompt
   includes the catalog of names + descriptions; the model loads bodies
   on demand. Useful when you don't know up-front which skill applies.

All three compose. Mid-run model-activated skills survive `/continue`
because they're embedded in the persisted message history.

### Example: install caveman + WNP-Loop

```
mkdir -p ~/.localagent/skills/caveman
mkdir -p ~/.localagent/skills/wnp-loop

# Copy SKILL.md files from their respective repos:
#   https://github.com/JuliusBrussee/caveman
#   https://github.com/PaulMDemers/WNP-Loop
```

Open the UI, click the checkboxes for caveman and wnp-loop, run the agent.

### Caveats

- **Markdown-only.** Scripts/executables bundled with a skill (the way
  Claude Code's `pdf` / `docx` / `xlsx` skills ship Python helpers) are
  **not** supported. The `SKILL.md` instructions can still tell the agent
  to write or run scripts itself via `run_command`.
- **Skill bodies are added to the system prompt verbatim** — keep them
  reasonable. A 50KB SKILL.md eats 50KB of every turn's context until
  compaction sees fit to summarize it.
- **Skills run alongside compaction.** The compactor will eventually
  summarize the skill body if it bloats older turns. To prevent that, keep
  active skill instructions tight.

## How the agent runs (todo-driven)

The loop is **not** capped at a fixed iteration count any more. Instead the
agent generates a plan and works through it:

1. **Plan.** The system prompt tells the model: *"call `update_todos` with
   3-8 concrete milestones for the user's goal — that's required before
   you do anything else."*
2. **Execute.** The model picks the first todo, marks it `in_progress`,
   does the work (read files, write code, run commands).
3. **Update.** When a milestone is done, the model calls `update_todos`
   again — same list with that item flipped to `completed` and the next
   one to `in_progress`. New work that wasn't in the original plan?
   The model adds new todos.
4. **Finish.** When every todo is `completed`, the model calls `finish`
   with a summary. The run ends.

The UI shows a **Todos panel** above the events list, updating live with
each `update_todos` call. ○ = pending, ▸ = in progress, ✓ = completed.
Picking a past session from the sidebar restores the final plan.

### Why this instead of `max_iterations`?

- The number of iterations needed depends entirely on the task. A
  hardcoded cap means short tasks finish with iterations to spare and
  long tasks run out partway through.
- A self-generated plan exposes the agent's reasoning. You see what it
  thinks it needs to do before it does anything, and you can cancel
  early if the plan is wrong.
- It plays well with skills like [WNP-Loop](https://github.com/PaulMDemers/WNP-Loop)
  that depend on milestone-driven execution.

There's still a hidden safety cap (200 iterations) to prevent pathological
loops. You'll only hit it if something is genuinely wrong (the model
won't call `finish`, won't call tools, etc.). The CLI flag `-max-iter`
overrides it for advanced use.

## Human-in-the-loop (`ask_user`)

The agent can pause mid-run and ask you a question via the `ask_user` tool.
This is what makes skills like [WNP-Loop](https://github.com/PaulMDemers/WNP-Loop)
work — they require the agent to propose a milestone and wait for explicit
approval before executing.

**How it looks from the UI:**

1. The agent calls `ask_user(question: "...", options: [...]?)`.
2. A yellow question card appears in the timeline. A sticky prompt box
   slides up at the bottom of the events panel with the question, optional
   quick-pick buttons (one per `options` entry), and a freeform textarea.
3. You type an answer (or click an option). Ctrl/⌘+Enter sends.
4. The agent's tool call unblocks with your answer as the result, and
   the loop continues.
5. **Cancel run** while a question is pending also works — the wait
   returns with `context.Canceled` and the session ends cleanly.

**For skill authors:** instruct the model to call `ask_user` whenever it
needs human input. Examples from WNP-Loop:

```
- After proposing a milestone, call ask_user("Proceed with milestone 2:
  add tests for the parser?", options: ["proceed", "skip", "amend"])
  and wait for the user's response before doing any work.
```

**Cost considerations:** `ask_user` blocks the whole agent loop until you
answer. There's no time pressure on you, but the model isn't doing
anything during that wait. Use it for decisions that materially affect
what comes next, not for things the agent should figure out itself.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| `POST`   | `/api/run` | Start a session. Body: `{model, host, workdir, goal, compaction_model?, context_tokens?, skills?: string[], max_iterations?}` → `{session_id}`. `max_iterations` is the safety cap (default 200); the agent's todo list is the primary control. |
| `GET`    | `/api/skills?workdir=X` | Discovered skill catalog (names + descriptions + source) for the given workdir. |
| `GET`    | `/api/sessions` | List all sessions (in-memory + persisted), newest first. |
| `GET`    | `/api/sessions/{id}` | Summary + full event history for one session. |
| `DELETE` | `/api/sessions/{id}` | Cancel (if running) and permanently remove a session from memory + disk. |
| `GET`    | `/api/sessions/{id}/events` | **SSE stream.** Replays full history, then tails live. 15 s heartbeats. |
| `POST`   | `/api/sessions/{id}/cancel` | Cancel a running session. |
| `POST`   | `/api/sessions/{id}/answer` | Deliver a user answer to a pending `ask_user` question. Body: `{question_id, answer}` → `{ok: true}` on success, 409 if not pending. |
| `POST`   | `/api/sessions/{id}/continue` | Run more iterations on a finished session with a follow-up instruction. Body: `{goal, host?, compaction_model?, context_tokens?, skills?: string[], max_iterations?}`. Reuses the session's model + workdir + LLM conversation; compaction + skill settings fall back to the session's saved values if omitted. The existing todo list persists — the agent can add to it or work through the remainder. |
| `GET`    | `/` | Embedded React UI. |

CORS is wide-open (`*`), so you can run a separate Vite/Next dev server against
the same API during UI development.

## Sessions

Every run becomes a **session** identified by an 8-byte hex ID. The server
persists the full event history of each finished session to disk as a single
JSON file, atomically (`tmp` + rename). The UI sidebar shows all sessions
newest-first with a status dot, and a `×` button deletes a session (both
in-memory state and the on-disk file).

| Knob | Default |
|---|---|
| Data directory | `~/.localagent/sessions/` (override with `-data-dir`) |
| Format | one `<id>.json` per session, pretty-printed |
| When written | on terminal event (`finished` / `error` / `canceled` / `max_iter`) |
| In-progress runs | held in memory only; lost if the server crashes mid-run |

To inspect a session by hand:

```
cat ~/.localagent/sessions/<id>.json | jq
```

## Project layout

```
.
├── main.go                       CLI entry + flag parsing, -serve vs CLI mode
├── web/
│   ├── index.html                React single-page UI (CDN React + Babel)
│   └── embed.go                  //go:embed bridge
└── internal/
    ├── llm/
    │   ├── client.go             voocel/litellm Ollama wrapper
    │   └── ollama.go             /api/ps + /api/show probe for effective num_ctx
    ├── tools/tools.go            6 sandboxed tools, provider-agnostic
    ├── skills/skills.go          SKILL.md discovery + frontmatter parser + slash-command parser
    ├── agent/
    │   ├── events.go             Event types (started/iteration/tool_call/compaction/skill_activated/question/todo_update/…)
    │   ├── compactor.go          3-stage context compactor (elide → summarize → trim)
    │   ├── skill_tool.go         activate_skill tool + prompt rendering helpers
    │   ├── ask_tool.go           ask_user tool for human-in-the-loop pauses
    │   ├── todos.go              update_todos tool + Todo type + validation
    │   └── agent.go              Loop: chat → tool calls → results → compact → repeat
    └── server/
        ├── server.go             HTTP handlers + SSE + Ollama context probe + skills discovery
        ├── sessions.go           Multi-subscriber session manager
        └── store.go              On-disk JSON session persistence
```

## Troubleshooting

- **`llm call failed: ... 404`** — model isn't pulled. Run `ollama pull <model>`.
- **Model never calls a tool** — the model doesn't support tool calling. Switch
  to one of the recommended models above.
- **`path escapes workdir`** — the model tried an absolute path or `..`. Expected
  behaviour; the agent will retry with a relative path.
- **UI shows `error` immediately after Start** — check the server log; usually
  workdir doesn't exist or the Ollama host is unreachable.
- **`ollama stalled: no chunks in 60s`** — Ollama still has the model loaded
  but stopped generating. Restart Ollama (`ollama serve` again) and retry.
  The agent uses streaming with a 60 s idle watchdog, so these stalls now
  surface as errors instead of hanging.
- **`⚠ Ollama will only serve N tokens for <model>`** — your **Context window**
  setting exceeds what Ollama is configured to serve. The compactor was
  clamped to Ollama's effective limit; see the **Context window** section
  above for how to raise it via a Modelfile or `OLLAMA_CONTEXT_LENGTH`.
- **Model output gets weirdly truncated / forgets earlier instructions** —
  almost always num_ctx; see above. The probe catches the common cases but
  can't detect every Modelfile/env-var combination.

## Notes

- voocel/litellm talks to Ollama's OpenAI-compatible endpoint (`/v1`), not the
  native `/api/chat`. Pass the literal Ollama model id (e.g. `llama3.1`), not
  `ollama/llama3.1`.
- Sessions live in memory only — restarting the server drops history.
- The CORS policy and lack of auth make this suitable for local use only. Do
  not expose `:8080` to a network you don't trust.
