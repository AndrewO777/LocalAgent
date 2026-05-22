// devloop runs the Go server and the Vite dev server side-by-side, with the
// same lifetime: ctrl+C kills both, and if either crashes the other gets
// torn down too.
//
// This replaces the bash-isms (`trap 'kill 0' INT`, `&`, line continuations)
// that used to live in the `make dev` recipe and didn't work on Windows when
// Make invoked the recipe via cmd.exe.
//
// Invocation:
//
//	go run ./scripts/devloop
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// procSpec is one child process: command + working dir + a colour code used
// to prefix its output lines so the merged stdout is easy to read.
type procSpec struct {
	name  string   // short label shown in the prefix
	color string   // ANSI colour code, e.g. "32" green
	dir   string   // cwd; "" = inherit
	argv  []string // command + args, argv[0] resolved via PATH
}

func main() {
	procs := []procSpec{
		{name: "go ", color: "32", dir: "", argv: []string{"go", "run", ".", "-serve", "-addr", ":8080"}},
		{name: "ui ", color: "36", dir: "web", argv: []string{"npm", "run", "dev"}},
	}

	// One context cancelled on signal OR when any child dies — that
	// way you don't end up with vite running while the Go server is
	// stopped, or vice versa.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Fprintln(os.Stderr, "devloop: starting Go (:8080) and Vite (:5173); ctrl+C to stop both")

	var wg sync.WaitGroup
	wg.Add(len(procs))
	for _, p := range procs {
		go func(p procSpec) {
			defer wg.Done()
			defer cancel() // any exit triggers shutdown of the other
			run(ctx, p)
		}(p)
	}
	wg.Wait()
	fmt.Fprintln(os.Stderr, "devloop: both processes exited")
}

// run spawns one child and pumps its stdout/stderr through the line-prefixer.
// Returns when the child exits (cleanly or via cancellation).
func run(ctx context.Context, p procSpec) {
	cmd := exec.CommandContext(ctx, p.argv[0], p.argv[1:]...)
	if p.dir != "" {
		cmd.Dir = p.dir
	}
	// OS-specific: set up a process group on Unix / new console group on
	// Windows so we can tree-kill the child + its grandchildren (esbuild,
	// node workers, etc.) on cancellation.
	setProcessAttrs(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] StdoutPipe: %v\n", p.name, err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] StderrPipe: %v\n", p.name, err)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] start: %v (is %q installed?)\n", p.name, err, p.argv[0])
		return
	}

	// Two pumps, one per pipe. The prefixer is goroutine-safe by virtue of
	// using fmt.Printf, which holds an internal mutex.
	var pumps sync.WaitGroup
	pumps.Add(2)
	go pump(&pumps, stdout, p.name, p.color)
	go pump(&pumps, stderr, p.name, p.color)
	pumps.Wait()

	if err := cmd.Wait(); err != nil {
		// Cancellation isn't really an error here; suppress the noisy
		// "signal: terminated" output.
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "[%s] exited: %v\n", p.name, err)
		}
	}
}

func pump(wg *sync.WaitGroup, r io.Reader, name, color string) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Printf("\x1b[%sm[%s]\x1b[0m %s\n", color, name, sc.Text())
	}
}
