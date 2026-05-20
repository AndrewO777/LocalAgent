package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Tool is a provider-agnostic description of a callable tool. The Parameters
// field is a JSON-schema-shaped map[string]any consumed by the LLM gateway.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     func(ctx context.Context, args string) (string, error)
}

// Registry is a name -> Tool map plus an ordered list for prompt construction.
type Registry struct {
	byName  map[string]Tool
	list    []Tool
	workdir string
}

func (r *Registry) List() []Tool   { return r.list }
func (r *Registry) Workdir() string { return r.workdir }

func (r *Registry) Call(ctx context.Context, name, args string) (string, error) {
	t, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Handler(ctx, args)
}

type sentinel struct{ msg string }

func (s *sentinel) Error() string { return s.msg }

// ErrFinished is returned by the finish tool to signal loop termination.
var ErrFinished = &sentinel{msg: "agent finished"}

// Build constructs the full toolset rooted at workdir. All file paths from the
// model are resolved relative to workdir and clamped inside it.
func Build(workdir string) (*Registry, error) {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("workdir %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workdir %q is not a directory", abs)
	}

	tools := []Tool{
		listDirTool(abs),
		readFileTool(abs),
		writeFileTool(abs),
		editFileTool(abs),
		runCommandTool(abs),
		finishTool(),
	}
	reg := &Registry{byName: make(map[string]Tool, len(tools)), list: tools, workdir: abs}
	for _, t := range tools {
		reg.byName[t.Name] = t
	}
	return reg, nil
}

// resolve clamps p inside root and returns an absolute path. Rejects absolute
// paths and anything that climbs out via "..".
func resolve(root, p string) (string, error) {
	if p == "" {
		return root, nil
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute paths not allowed: %s", p)
	}
	full := filepath.Join(root, p)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workdir: %s", p)
	}
	return full, nil
}

func decode(args string, dst any) error {
	if args == "" {
		args = "{}"
	}
	dec := json.NewDecoder(strings.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// ----- list_dir ------------------------------------------------------------

func listDirTool(root string) Tool {
	return Tool{
		Name:        "list_dir",
		Description: "List the contents of a directory inside the project. Returns one entry per line with a trailing '/' for directories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path relative to the project root. Use \"\" or \".\" for the root.",
				},
			},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := decode(args, &in); err != nil {
				return "", err
			}
			dir, err := resolve(root, in.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", err
			}
			if len(entries) == 0 {
				return "(empty directory)", nil
			}
			var b strings.Builder
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				b.WriteString(name)
				b.WriteByte('\n')
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// ----- read_file -----------------------------------------------------------

const maxReadBytes = 200 * 1024

func readFileTool(root string) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a UTF-8 text file inside the project. Truncated at ~200 KB.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path relative to the project root.",
				},
			},
			"required": []string{"path"},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := decode(args, &in); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Path) == "" {
				return "", errors.New("path is required (relative file path inside the project)")
			}
			full, err := resolve(root, in.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			truncated := false
			if len(data) > maxReadBytes {
				data = data[:maxReadBytes]
				truncated = true
			}
			out := string(data)
			if truncated {
				out += "\n[...truncated]"
			}
			return out, nil
		},
	}
}

// ----- write_file ----------------------------------------------------------

func writeFileTool(root string) Tool {
	return Tool{
		Name:        "write_file",
		Description: "Create or overwrite a file inside the project. Creates parent directories as needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path relative to the project root."},
				"content": map[string]any{"type": "string", "description": "Full file contents to write."},
			},
			"required": []string{"path", "content"},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			var in struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decode(args, &in); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Path) == "" {
				return "", errors.New("path is required (relative file path inside the project — do NOT omit it)")
			}
			full, err := resolve(root, in.Path)
			if err != nil {
				return "", err
			}
			if info, err := os.Stat(full); err == nil && info.IsDir() {
				return "", fmt.Errorf("path %q is an existing directory, not a file", in.Path)
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(full, []byte(in.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), in.Path), nil
		},
	}
}

// ----- edit_file -----------------------------------------------------------

func editFileTool(root string) Tool {
	return Tool{
		Name:        "edit_file",
		Description: "Replace the first occurrence of old_text with new_text in an existing file. Use unique surrounding context in old_text so the match is unambiguous.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "description": "File path relative to the project root."},
				"old_text": map[string]any{"type": "string", "description": "Exact text to find."},
				"new_text": map[string]any{"type": "string", "description": "Replacement text."},
			},
			"required": []string{"path", "old_text", "new_text"},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			var in struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := decode(args, &in); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Path) == "" {
				return "", errors.New("path is required (relative file path inside the project)")
			}
			if in.OldText == "" {
				return "", errors.New("edit_file requires non-empty old_text — it is the exact text to find and replace. To create a new file or fully replace an existing file's contents, call write_file with the full contents instead.")
			}
			full, err := resolve(root, in.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			text := string(data)
			idx := strings.Index(text, in.OldText)
			if idx < 0 {
				return "", errors.New("old_text not found in file")
			}
			if strings.Count(text, in.OldText) > 1 {
				return "", errors.New("old_text matches multiple locations — expand the context so the match is unique")
			}
			updated := text[:idx] + in.NewText + text[idx+len(in.OldText):]
			if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("edited %s", in.Path), nil
		},
	}
}

// ----- run_command ---------------------------------------------------------

const (
	defaultCmdTimeoutSec = 120
	maxCmdTimeoutSec     = 600
	maxCmdOutput         = 32 * 1024
	// waitDelay is the grace period between Cancel firing and the I/O pipes
	// being force-closed. Without it, hung children keep the pipes open and
	// CombinedOutput blocks forever even after the kill signal.
	waitDelay = 3 * time.Second
)

func runCommandTool(root string) Tool {
	return Tool{
		Name: "run_command",
		Description: "Run a shell command inside the project directory and wait for it to exit. " +
			"Output is merged stdout+stderr, truncated at ~32 KB. Default timeout is 120 s; " +
			"pass timeout_sec up to 600 to extend. Do NOT use this to start long-running " +
			"foreground servers (npm run dev, npx vite, any --watch process) — they will " +
			"hit the timeout and be killed. Use build/test/lint commands that exit on their own.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command line. Runs via PowerShell on Windows and /bin/sh elsewhere.",
				},
				"timeout_sec": map[string]any{
					"type":        "integer",
					"description": "Optional timeout in seconds. Default 120, max 600.",
				},
			},
			"required": []string{"command"},
		},
		Handler: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Command    string `json:"command"`
				TimeoutSec int    `json:"timeout_sec"`
			}
			if err := decode(args, &in); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Command) == "" {
				return "", errors.New("command is empty")
			}
			timeoutSec := in.TimeoutSec
			if timeoutSec <= 0 {
				timeoutSec = defaultCmdTimeoutSec
			}
			if timeoutSec > maxCmdTimeoutSec {
				timeoutSec = maxCmdTimeoutSec
			}

			cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", in.Command)
			} else {
				cmd = exec.CommandContext(cmdCtx, "/bin/sh", "-c", in.Command)
			}
			cmd.Dir = root
			setProcessAttrs(cmd)
			// Override CommandContext's default Cancel (which only kills the
			// direct child) with a tree kill. WaitDelay forces the stdout/stderr
			// pipes closed if the kill doesn't drain them — without it,
			// CombinedOutput blocks forever when grandchildren keep the pipe open.
			cmd.Cancel = func() error { return killProcessTree(cmd) }
			cmd.WaitDelay = waitDelay

			out, runErr := cmd.CombinedOutput()
			body := out
			if len(body) > maxCmdOutput {
				body = body[:maxCmdOutput]
			}
			var b strings.Builder
			if runErr != nil {
				switch {
				case cmdCtx.Err() == context.DeadlineExceeded:
					fmt.Fprintf(&b, "[timed out after %ds — process tree killed]\n", timeoutSec)
				case ctx.Err() == context.Canceled:
					b.WriteString("[canceled — process tree killed]\n")
				default:
					fmt.Fprintf(&b, "[exit error: %v]\n", runErr)
				}
			}
			b.Write(body)
			if len(out) > maxCmdOutput {
				b.WriteString("\n[...truncated]")
			}
			result := strings.TrimRight(b.String(), "\n")
			if result == "" {
				result = "(no output)"
			}
			return result, nil
		},
	}
}

// ----- finish --------------------------------------------------------------

func finishTool() Tool {
	return Tool{
		Name:        "finish",
		Description: "Call this when the user's task is complete. Provide a short summary of what was done.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "Short summary of the work completed.",
				},
			},
			"required": []string{"summary"},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			var in struct {
				Summary string `json:"summary"`
			}
			_ = decode(args, &in)
			return in.Summary, ErrFinished
		},
	}
}
