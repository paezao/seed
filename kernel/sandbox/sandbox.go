// Package sandbox runs generated code in isolation.
//
// The kernel never executes organism code or agent-issued shell commands on
// the host. Everything goes through a Sandbox: a container whose only view of
// the filesystem is the directory it was created for (read-only except for
// the directories it may change), on a private network, with resource limits.
package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Spec describes a sandbox to create.
type Spec struct {
	// Name identifies the sandbox (container name for Docker).
	Name string
	// Root is the host directory exposed to the sandbox as its working directory.
	Root string
	// Writable lists directories (relative to Root) that sandboxed code may
	// modify. When non-empty, everything else under Root is read-only.
	Writable []string
	// Hidden lists paths (relative to Root) masked by an empty tmpfs.
	Hidden []string
	// Env is set for every command and process.
	Env map[string]string
	// Port is the port that processes listen on inside the sandbox (exported as PORT).
	Port int
	// Labels are attached to the sandbox for bookkeeping/cleanup.
	Labels map[string]string
	// PrivateNetwork runs long-lived processes (Start) in their own network
	// namespace: nothing else can reach them over TCP, and the kernel talks to
	// them through a Unix socket (Transport). Used for the live organism, so
	// experimental code can never call it. Exec commands keep network access
	// (builds install dependencies).
	PrivateNetwork bool
}

type ExecResult struct {
	Output   string        `json:"output"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
	TimedOut bool          `json:"timed_out,omitempty"`
}

func (r ExecResult) OK() bool { return r.ExitCode == 0 && !r.TimedOut }

// Sandbox is an isolated execution environment.
type Sandbox interface {
	// Exec runs a shell command to completion in the sandbox's root.
	Exec(ctx context.Context, command string, timeout time.Duration, env map[string]string) (ExecResult, error)
	// Start launches a named long-running background process.
	Start(ctx context.Context, name, command string, env map[string]string) error
	// Stop terminates a named background process.
	Stop(ctx context.Context, name string) error
	// Running reports whether a named background process is alive.
	Running(ctx context.Context, name string) bool
	// Logs returns the last lines of a background process's output.
	Logs(ctx context.Context, name string, tail int) string
	// URL is the base URL for Spec.Port, used with Transport.
	URL(ctx context.Context) (string, error)
	// Transport reaches the sandbox's processes (a Unix socket bridge for
	// PrivateNetwork sandboxes).
	Transport() http.RoundTripper
	// Alive reports whether the sandbox itself still exists.
	Alive(ctx context.Context) bool
	// Close destroys the sandbox.
	Close(ctx context.Context) error
}

// Driver creates sandboxes.
type Driver interface {
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	// Isolated reports whether this driver provides real isolation.
	Isolated() bool
}

const maxOutput = 2 << 20

// limitedBuffer keeps the head and tail of large outputs.
type limitedBuffer struct {
	head, tail []byte
	dropped    int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.head) < maxOutput/2 {
		room := maxOutput/2 - len(b.head)
		if room > len(p) {
			room = len(p)
		}
		b.head = append(b.head, p[:room]...)
		p = p[room:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if over := len(b.tail) - maxOutput/2; over > 0 {
			b.dropped += over
			b.tail = b.tail[over:]
		}
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	if b.dropped == 0 {
		return string(b.head) + string(b.tail)
	}
	return fmt.Sprintf("%s\n…[%d bytes omitted]…\n%s", b.head, b.dropped, b.tail)
}

func validName(name string) error {
	if name == "" || strings.ContainsAny(name, "/ \t\n'\"$`;&|") {
		return fmt.Errorf("invalid process name %q", name)
	}
	return nil
}
