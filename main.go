package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/andrew/localagent/internal/agent"
	"github.com/andrew/localagent/internal/llm"
	"github.com/andrew/localagent/internal/server"
	"github.com/andrew/localagent/internal/tools"
	"github.com/andrew/localagent/web"
)

func main() {
	var (
		serve   = flag.Bool("serve", false, "Run as an HTTP server + web UI instead of one-shot CLI.")
		addr    = flag.String("addr", ":8080", "Address to listen on when -serve is set.")
		model   = flag.String("model", "qwen2.5-coder:7b", "Ollama model name (must support tool calling: qwen2.5-coder, llama3.1, mistral-nemo, etc.)")
		host    = flag.String("host", "", "Ollama server URL (default http://localhost:11434)")
		workdir = flag.String("workdir", ".", "Project directory the agent operates inside (CLI mode only)")
		goal    = flag.String("goal", "", "Task for the agent (CLI mode). If empty, reads from stdin.")
		maxIter = flag.Int("max-iter", 25, "Maximum agent iterations before giving up (CLI mode default).")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `LocalAgent — agentic coding loop backed by a local Ollama model.

Modes:
  Server:  LocalAgent -serve [-addr :8080]
  CLI:     LocalAgent -workdir ./project -goal "task description"

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *serve {
		runServer(*addr, *host)
		return
	}
	runCLI(*model, *host, *workdir, *goal, *maxIter)
}

// ---- server mode ----------------------------------------------------------

func runServer(addr, defaultHost string) {
	if defaultHost == "" {
		defaultHost = "http://localhost:11434"
	}
	srv := server.New(web.FS(), defaultHost)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		fmt.Fprintf(os.Stderr, "LocalAgent listening on http://localhost%s (default Ollama host: %s)\n", addr, defaultHost)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "server: %v\n", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// ---- CLI mode -------------------------------------------------------------

func runCLI(model, host, workdir, goal string, maxIter int) {
	task := strings.TrimSpace(goal)
	if task == "" {
		fmt.Fprint(os.Stderr, "Enter task for the agent (end with blank line):\n> ")
		task = readMultiline(os.Stdin)
		if task == "" {
			fmt.Fprintln(os.Stderr, "no goal provided")
			os.Exit(2)
		}
	}

	reg, err := tools.Build(workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tools: %v\n", err)
		os.Exit(1)
	}

	client, err := llm.New(model, host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "model: %s   workdir: %s   max-iter: %d\n", model, workdir, maxIter)

	err = agent.Run(ctx, agent.Config{
		LLM:           client,
		Tools:         reg,
		Goal:          task,
		MaxIterations: maxIter,
		Emit:          printEvent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nagent: %v\n", err)
		os.Exit(1)
	}
}

func printEvent(e agent.Event) {
	switch e.Type {
	case agent.EventStarted:
		fmt.Fprintf(os.Stderr, "▶ goal: %s\n", e.Text)
	case agent.EventIteration:
		fmt.Fprintf(os.Stderr, "\n── iteration %d ──\n", e.Iter)
	case agent.EventModelText:
		fmt.Fprintf(os.Stderr, "model: %s\n", e.Text)
	case agent.EventToolCall:
		fmt.Fprintf(os.Stderr, "→ %s %s\n", e.Tool, oneLine(prettyJSON(e.Arguments)))
	case agent.EventToolResult:
		prefix := "  "
		if e.IsError {
			prefix = "✗ "
		}
		fmt.Fprintf(os.Stderr, "%s%s\n", prefix, oneLine(e.Result))
	case agent.EventDone:
		switch e.Reason {
		case agent.ReasonFinished:
			fmt.Fprintf(os.Stderr, "\n✓ finished: %s\n", e.Summary)
		case agent.ReasonMaxIter:
			fmt.Fprintln(os.Stderr, "\n⚠ reached max iterations")
		case agent.ReasonCanceled:
			fmt.Fprintln(os.Stderr, "\n⚠ canceled")
		case agent.ReasonError:
			fmt.Fprintf(os.Stderr, "\n✗ error: %s\n", e.Text)
		}
	}
}

func prettyJSON(s string) string {
	if s == "" {
		return ""
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func readMultiline(r *os.File) string {
	sc := bufio.NewScanner(r)
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
