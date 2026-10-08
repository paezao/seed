package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LocalDriver runs commands directly on the host in Spec.Root.
//
// It provides NO isolation and exists for kernel tests and for environments
// where the owner explicitly opts out of Docker (sandbox.driver: local).
type LocalDriver struct{}

func (LocalDriver) Isolated() bool { return false }

func (LocalDriver) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	return newLocal(spec)
}

func newLocal(spec Spec) (*localSandbox, error) {
	if spec.Root == "" {
		return nil, errors.New("sandbox: root is required")
	}
	procDir, err := os.MkdirTemp("", "seed-proc-")
	if err != nil {
		return nil, err
	}
	s := &localSandbox{spec: spec, procDir: procDir, procs: map[string]*exec.Cmd{}, env: map[string]string{}}
	for k, v := range spec.Env {
		s.env[k] = v
	}
	if spec.Port != 0 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		s.hostPort = l.Addr().(*net.TCPAddr).Port
		l.Close()
		s.env["PORT"] = strconv.Itoa(s.hostPort)
	}
	return s, nil
}

type localSandbox struct {
	spec     Spec
	procDir  string
	hostPort int
	env      map[string]string
	mu       sync.Mutex
	procs    map[string]*exec.Cmd
	closed   bool
	// command builds the process for a shell command line; the local driver
	// runs it directly, other drivers wrap it (e.g. in bubblewrap). start, if
	// set, builds long-lived processes (Start).
	command func(line string, extra map[string]string) *exec.Cmd
	start   func(line string, extra map[string]string) *exec.Cmd
	// bridge is the Unix socket reaching a PrivateNetwork process.
	bridge string
}

func (s *localSandbox) Transport() http.RoundTripper {
	if s.bridge == "" {
		return &http.Transport{DisableKeepAlives: true}
	}
	sock := s.bridge
	return &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
}

func (s *localSandbox) cmd(line string, extra map[string]string) *exec.Cmd {
	if s.command != nil {
		return s.command(line, extra)
	}
	c := exec.Command("bash", "-c", line)
	c.Dir = s.spec.Root
	c.Env = s.environ(extra)
	return c
}

func (s *localSandbox) environ(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range s.env {
		env = append(env, k+"="+v)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func (s *localSandbox) Exec(ctx context.Context, command string, timeout time.Duration, env map[string]string) (ExecResult, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := s.cmd(command, env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-cctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-done
		timedOut = ctx.Err() == nil
		if !timedOut {
			return ExecResult{Output: out.String(), Duration: time.Since(start)}, ctx.Err()
		}
	}
	res := ExecResult{Output: out.String(), Duration: time.Since(start), TimedOut: timedOut}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 137
		}
	} else if err != nil {
		return res, err
	}
	return res, nil
}

func (s *localSandbox) Start(ctx context.Context, name, command string, env map[string]string) error {
	if err := validName(name); err != nil {
		return err
	}
	_ = s.Stop(ctx, name)
	logf, err := os.Create(filepath.Join(s.procDir, name+".log"))
	if err != nil {
		return err
	}
	cmd := s.cmd(command, env)
	if s.start != nil {
		cmd = s.start(command, env)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	s.mu.Lock()
	s.procs[name] = cmd
	s.mu.Unlock()
	go func() { _ = cmd.Wait(); logf.Close() }()
	return nil
}

func (s *localSandbox) Stop(_ context.Context, name string) error {
	s.mu.Lock()
	cmd := s.procs[name]
	delete(s.procs, name)
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if syscall.Kill(cmd.Process.Pid, 0) != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return nil
}

func (s *localSandbox) Running(_ context.Context, name string) bool {
	s.mu.Lock()
	cmd := s.procs[name]
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return false
	}
	if cmd.ProcessState != nil {
		return false
	}
	return syscall.Kill(cmd.Process.Pid, 0) == nil
}

func (s *localSandbox) Logs(_ context.Context, name string, tail int) string {
	b, err := os.ReadFile(filepath.Join(s.procDir, name+".log"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n")
}

func (s *localSandbox) URL(context.Context) (string, error) {
	if s.bridge != "" {
		return "http://organism", nil
	}
	if s.hostPort == 0 {
		return "", errors.New("sandbox publishes no port")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", s.hostPort), nil
}

func (s *localSandbox) Alive(context.Context) bool { return !s.closed }

func (s *localSandbox) Close(ctx context.Context) error {
	s.mu.Lock()
	names := make([]string, 0, len(s.procs))
	for n := range s.procs {
		names = append(names, n)
	}
	s.mu.Unlock()
	for _, n := range names {
		_ = s.Stop(ctx, n)
	}
	s.closed = true
	return os.RemoveAll(s.procDir)
}
