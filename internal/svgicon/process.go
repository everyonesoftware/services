package svgicon

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"io"
	"os"
	"os/exec"
	"time"
)

// Two active render children process-wide. Queueing and execution share a hard
// three-second deadline; cancellation kills the child rather than abandoning a
// rendering goroutine. Input validation is bounded and done before spawning.
const RenderTimeout = 3 * time.Second
const RendererConcurrency = 2

var renderer = processRenderer{slots: make(chan struct{}, RendererConcurrency), timeout: RenderTimeout, command: workerCommand}

type processRenderer struct {
	slots   chan struct{}
	timeout time.Duration
	command func(context.Context) *exec.Cmd // trusted test seam, not caller controlled
}

func workerCommand(ctx context.Context) *exec.Cmd {
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	return exec.CommandContext(ctx, executable, WorkerFlag)
}

// Keep the buffer named: embedding would promote ReadFrom, letting io.Copy
// bypass Write and its output limit when draining a child process pipe.
type limitedOutput struct{ buffer bytes.Buffer }

func (b *limitedOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > MaxPNGBytes {
		return 0, errors.New("SVG worker output limit")
	}
	return b.buffer.Write(p)
}

// Normalize validates in the caller; the child independently validates again.
func Normalize(ctx context.Context, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clean, _, _, err := validate(data)
	if err != nil {
		return nil, err
	}
	return renderer.render(ctx, clean)
}
func (r *processRenderer) render(ctx context.Context, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.slots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := r.command(ctx)
	if cmd == nil {
		return nil, errors.New("SVG worker unavailable")
	}
	// No inherited application secrets, proxy configuration, or loader paths.
	// SystemRoot is needed by Windows process/runtime initialization only.
	cmd.Env = []string{"GOMAXPROCS=1", "GOMEMLIMIT=64MiB"}
	if root := os.Getenv("SystemRoot"); root != "" {
		cmd.Env = append(cmd.Env, "SystemRoot="+root)
	}
	cmd.Stdin = bytes.NewReader(data)
	var out limitedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 200 * time.Millisecond
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errors.New("SVG rendering failed")
	}
	config, err := png.DecodeConfig(bytes.NewReader(out.Bytes()))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 512 || config.Height > 512 {
		return nil, errors.New("invalid SVG worker PNG")
	}
	if _, err := png.Decode(bytes.NewReader(out.Bytes())); err != nil {
		return nil, errors.New("invalid SVG worker PNG")
	}
	return out.Bytes(), nil
}
