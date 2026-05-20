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
| Max iterations | `25` | Hard cap on the tool-call loop. |
| Context window | `32768` | Token budget the compactor protects. Set to `0` to disable compaction entirely. See **Context window** below. |

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
-model     Ollama model id           (default qwen2.5-coder:7b)
-host      Ollama base URL           (default http://localhost:11434)
-max-iter  iteration cap             (default 25)
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
| `finish(summary)` | Signal task complete. |

Sandboxing rules: absolute paths and `..` escapes are rejected at the tool
boundary.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| `POST`   | `/api/run` | Start a session. Body: `{model, host, workdir, goal, max_iterations, compaction_model?, context_tokens?}` → `{session_id}`. |
| `GET`    | `/api/sessions` | List all sessions (in-memory + persisted), newest first. |
| `GET`    | `/api/sessions/{id}` | Summary + full event history for one session. |
| `DELETE` | `/api/sessions/{id}` | Cancel (if running) and permanently remove a session from memory + disk. |
| `GET`    | `/api/sessions/{id}/events` | **SSE stream.** Replays full history, then tails live. 15 s heartbeats. |
| `POST`   | `/api/sessions/{id}/cancel` | Cancel a running session. |
| `POST`   | `/api/sessions/{id}/continue` | Run more iterations on a finished session with a follow-up instruction. Body: `{goal, max_iterations, host?, compaction_model?, context_tokens?}`. Reuses the session's model + workdir + LLM conversation; compaction settings fall back to the session's saved values if omitted. |
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
    ├── agent/
    │   ├── events.go             Event types (started/iteration/tool_call/compaction/…)
    │   ├── compactor.go          3-stage context compactor (elide → summarize → trim)
    │   └── agent.go              Loop: chat → tool calls → results → compact → repeat
    └── server/
        ├── server.go             HTTP handlers + SSE + Ollama context probe
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
