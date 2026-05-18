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
| Goal | *(required)* | Natural-language task for the agent. |
| Max iterations | `25` | Hard cap on the tool-call loop. |

Click **Run agent**. Iteration headers, model output, tool calls, and tool
results stream in via Server-Sent Events. **Cancel run** aborts mid-loop.

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
| `run_command(command)` | Run a shell command. PowerShell on Windows, `/bin/sh -c` elsewhere. 2-minute timeout, 32 KB output cap, cwd locked to workdir. |
| `finish(summary)` | Signal task complete. |

Sandboxing rules: absolute paths and `..` escapes are rejected at the tool
boundary.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/run` | Start a session. Body: `{model, host, workdir, goal, max_iterations}` → `{session_id}`. |
| `GET`  | `/api/sessions` | List all sessions. |
| `GET`  | `/api/sessions/{id}` | Summary for one session. |
| `GET`  | `/api/sessions/{id}/events` | **SSE stream.** Replays full history, then tails live. 15 s heartbeats. |
| `POST` | `/api/sessions/{id}/cancel` | Cancel a running session. |
| `GET`  | `/` | Embedded React UI. |

CORS is wide-open (`*`), so you can run a separate Vite/Next dev server against
the same API during UI development.

## Project layout

```
.
├── main.go                       CLI entry + flag parsing, -serve vs CLI mode
├── web/
│   ├── index.html                React single-page UI (CDN React + Babel)
│   └── embed.go                  //go:embed bridge
└── internal/
    ├── llm/client.go             voocel/litellm Ollama wrapper
    ├── tools/tools.go            6 sandboxed tools, provider-agnostic
    ├── agent/
    │   ├── events.go             Event types (started/iteration/tool_call/…)
    │   └── agent.go              Loop: chat → tool calls → results → repeat
    └── server/
        ├── server.go             HTTP handlers + SSE
        └── sessions.go           Multi-subscriber session manager
```

## Troubleshooting

- **`llm call failed: ... 404`** — model isn't pulled. Run `ollama pull <model>`.
- **Model never calls a tool** — the model doesn't support tool calling. Switch
  to one of the recommended models above.
- **`path escapes workdir`** — the model tried an absolute path or `..`. Expected
  behaviour; the agent will retry with a relative path.
- **UI shows `error` immediately after Start** — check the server log; usually
  workdir doesn't exist or the Ollama host is unreachable.

## Notes

- voocel/litellm talks to Ollama's OpenAI-compatible endpoint (`/v1`), not the
  native `/api/chat`. Pass the literal Ollama model id (e.g. `llama3.1`), not
  `ollama/llama3.1`.
- Sessions live in memory only — restarting the server drops history.
- The CORS policy and lack of auth make this suitable for local use only. Do
  not expose `:8080` to a network you don't trust.
