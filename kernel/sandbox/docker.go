package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DockerDriver runs sandboxes as Docker containers.
//
// Isolation properties:
//   - only Spec.Root is mounted (at /workspace); ReadOnly paths are re-mounted :ro
//   - Hidden paths are masked with tmpfs
//   - runs as the host user's uid/gid (no root), all capabilities dropped,
//     no-new-privileges, pid/memory/cpu limits
//   - attached to a dedicated network shared only with PostgreSQL; ports are
//     published on 127.0.0.1 only
//   - no Docker socket, no host home directory, no secrets beyond the env given
type DockerDriver struct {
	Image   string
	Network string
	Memory  string
	CPUs    string
	// CacheDir is a host directory for Go/npm caches shared across sandboxes.
	CacheDir string
}

func (d *DockerDriver) Isolated() bool { return true }

func (d *DockerDriver) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	if spec.Name == "" || spec.Root == "" {
		return nil, errors.New("sandbox: name and root are required")
	}
	_, _ = docker(ctx, "rm", "-f", spec.Name) // stale container from a crash
	if err := os.MkdirAll(d.CacheDir, 0o755); err != nil {
		return nil, err
	}
	args := []string{"run", "-d", "--init", "--name", spec.Name,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "2048",
		"-w", "/workspace",
		"-v", spec.Root + ":/workspace",
		"-v", d.CacheDir + ":/cache",
		"-e", "HOME=/tmp/home",
		"-e", "GOPATH=/cache/go",
		"-e", "GOMODCACHE=/cache/go/pkg/mod",
		"-e", "GOCACHE=/cache/go-build",
		"-e", "npm_config_cache=/cache/npm",
		"-e", "npm_config_update_notifier=false",
		"-e", "CI=true",
	}
	if d.Network != "" {
		args = append(args, "--network", d.Network)
	}
	if d.Memory != "" {
		args = append(args, "--memory", d.Memory)
	}
	if d.CPUs != "" {
		args = append(args, "--cpus", d.CPUs)
	}
	for _, ro := range spec.ReadOnly {
		host := filepath.Join(spec.Root, ro)
		if _, err := os.Stat(host); err != nil {
			continue
		}
		args = append(args, "-v", host+":"+filepath.Join("/workspace", ro)+":ro")
	}
	for _, h := range spec.Hidden {
		if _, err := os.Stat(filepath.Join(spec.Root, h)); err != nil {
			continue
		}
		args = append(args, "--tmpfs", filepath.Join("/workspace", h))
	}
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	if spec.Port != 0 {
		env["PORT"] = strconv.Itoa(spec.Port)
		args = append(args, "-p", fmt.Sprintf("127.0.0.1::%d", spec.Port))
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	for k, v := range spec.Labels {
		args = append(args, "--label", k+"="+v)
	}
	args = append(args, d.Image, "sleep", "infinity")
	if _, err := docker(ctx, args...); err != nil {
		return nil, err
	}
	sb := &dockerSandbox{name: spec.Name, port: spec.Port}
	// Prepare HOME and the process directory.
	if _, err := sb.Exec(ctx, "mkdir -p /tmp/home /tmp/seed-proc", 30*time.Second, nil); err != nil {
		_ = sb.Close(ctx)
		return nil, err
	}
	return sb, nil
}

type dockerSandbox struct {
	name string
	port int
}

func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (s *dockerSandbox) Exec(ctx context.Context, command string, timeout time.Duration, env map[string]string) (ExecResult, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	secs := int(timeout.Seconds())
	args := []string{"exec", "-i"}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "-e", "SEED_CMD="+command, s.name,
		// coreutils timeout guarantees the in-container process dies with the deadline.
		"timeout", "--kill-after=10", strconv.Itoa(secs), "bash", "-c", `eval "$SEED_CMD"`)
	cctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(cctx, "docker", args...)
	cmd.Stdin = strings.NewReader("")
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	res := ExecResult{Output: out.String(), Duration: time.Since(start)}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			if res.ExitCode == 124 || res.ExitCode == 137 && res.Duration >= timeout {
				res.TimedOut = true
			}
			return res, nil
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, err
	}
	return res, nil
}

func (s *dockerSandbox) Start(ctx context.Context, name, command string, env map[string]string) error {
	if err := validName(name); err != nil {
		return err
	}
	_ = s.Stop(ctx, name)
	args := []string{"exec"}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	script := fmt.Sprintf(`setsid bash -c "$SEED_CMD" > /tmp/seed-proc/%[1]s.log 2>&1 < /dev/null & echo $! > /tmp/seed-proc/%[1]s.pid`, name)
	args = append(args, "-e", "SEED_CMD="+command, s.name, "bash", "-c", script)
	_, err := docker(ctx, args...)
	return err
}

func (s *dockerSandbox) Stop(ctx context.Context, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	script := fmt.Sprintf(`f=/tmp/seed-proc/%s.pid; [ -f $f ] || exit 0; p=$(cat $f); kill -TERM -- -$p 2>/dev/null || kill -TERM $p 2>/dev/null; for i in $(seq 1 50); do kill -0 $p 2>/dev/null || break; sleep 0.1; done; kill -KILL -- -$p 2>/dev/null; rm -f $f; true`, name)
	_, err := docker(ctx, "exec", s.name, "bash", "-c", script)
	return err
}

func (s *dockerSandbox) Running(ctx context.Context, name string) bool {
	if validName(name) != nil {
		return false
	}
	_, err := docker(ctx, "exec", s.name, "bash", "-c", fmt.Sprintf(`kill -0 $(cat /tmp/seed-proc/%s.pid 2>/dev/null) 2>/dev/null`, name))
	return err == nil
}

func (s *dockerSandbox) Logs(ctx context.Context, name string, tail int) string {
	if validName(name) != nil {
		return ""
	}
	out, _ := docker(ctx, "exec", s.name, "tail", "-n", strconv.Itoa(tail), "/tmp/seed-proc/"+name+".log")
	return out
}

func (s *dockerSandbox) URL(ctx context.Context) (string, error) {
	if s.port == 0 {
		return "", errors.New("sandbox publishes no port")
	}
	out, err := docker(ctx, "port", s.name, fmt.Sprintf("%d/tcp", s.port))
	if err != nil {
		return "", err
	}
	line := strings.SplitN(out, "\n", 2)[0]
	return "http://" + strings.TrimSpace(line), nil
}

func (s *dockerSandbox) Alive(ctx context.Context) bool {
	out, err := docker(ctx, "inspect", "-f", "{{.State.Running}}", s.name)
	return err == nil && out == "true"
}

func (s *dockerSandbox) Close(ctx context.Context) error {
	_, err := docker(context.WithoutCancel(ctx), "rm", "-f", s.name)
	return err
}
