package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// BwrapDriver isolates sandboxes with bubblewrap (Linux namespaces, no
// daemon, no root). It is the default inside a Seed's container: every
// command and process an evolution or the live organism runs gets
//   - a fresh mount namespace where the workspace is /workspace, read-only
//     except Spec.Writable, with Spec.Hidden masked;
//   - the rest of the filesystem read-only, and Hide paths (the live Seed,
//     its secrets, the owner's config) masked with empty tmpfs;
//   - its own PID, IPC and UTS namespaces, a private /tmp, a clean
//     environment, and (optionally) no network;
//   - the Seed's PostgreSQL only through its Unix socket, read-only, at /run/seed-db.
type BwrapDriver struct {
	// SocketDir is the PostgreSQL socket directory, bound at /run/seed-db.
	SocketDir string
	// CacheDir is a writable build cache (Go modules, npm), bound at /cache.
	CacheDir string
	// Hide lists absolute paths sandboxes must not see at all.
	Hide []string
	// NoNetwork cuts network access (dependency installs then fail).
	NoNetwork bool
}

// SandboxSocketDir is where sandboxes find the PostgreSQL socket.
const SandboxSocketDir = "/run/seed-db"

func (d *BwrapDriver) Isolated() bool { return true }

func (d *BwrapDriver) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	s, err := newLocal(spec)
	if err != nil {
		return nil, err
	}
	if d.CacheDir != "" {
		if err := os.MkdirAll(d.CacheDir, 0o755); err != nil {
			return nil, err
		}
	}
	for _, w := range spec.Writable {
		if err := os.MkdirAll(filepath.Join(spec.Root, w), 0o755); err != nil {
			return nil, err
		}
	}
	s.command = func(line string, extra map[string]string) *exec.Cmd {
		return exec.Command("bwrap", d.args(spec, s.env, extra, line, "")...)
	}
	if spec.PrivateNetwork {
		// The kernel's end of the bridge lives in its own /tmp, which no
		// sandbox can see (each gets a private /tmp).
		dir, err := os.MkdirTemp("", "seed-bridge-")
		if err != nil {
			return nil, err
		}
		s.bridge = filepath.Join(dir, "http.sock")
		s.start = func(line string, extra map[string]string) *exec.Cmd {
			// Inside the private namespace, socat bridges the socket to the
			// process's port; it goes away with the process.
			wrapped := `rm -f /run/organism/http.sock; socat UNIX-LISTEN:/run/organism/http.sock,fork,mode=600 TCP:127.0.0.1:$PORT & bridge=$!; trap 'kill $bridge 2>/dev/null' EXIT; ` + line
			return exec.Command("bwrap", d.args(spec, s.env, extra, wrapped, dir)...)
		}
	}
	return s, nil
}

// args builds the bubblewrap command line. Order matters: later mounts are
// placed on top of earlier ones.
func (d *BwrapDriver) args(spec Spec, base, extra map[string]string, line, bridgeDir string) []string {
	a := []string{
		"--die-with-parent", "--new-session",
		"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try",
	}
	// Start from an empty root and bring in only the system: never home
	// directories or other mounts the host may have.
	a = append(a, systemMounts()...)
	a = append(a,
		"--dev", "/dev", "--proc", "/proc",
		"--tmpfs", "/tmp", "--dir", "/tmp/home",
		"--tmpfs", "/run",
	)
	if d.NoNetwork || bridgeDir != "" {
		a = append(a, "--unshare-net")
	}
	for _, h := range d.Hide {
		if _, err := os.Stat(h); err == nil {
			a = append(a, "--tmpfs", h)
		}
	}
	a = append(a, "--ro-bind", spec.Root, "/workspace")
	for _, w := range spec.Writable {
		a = append(a, "--bind", filepath.Join(spec.Root, w), filepath.Join("/workspace", w))
	}
	for _, h := range spec.Hidden {
		if _, err := os.Stat(filepath.Join(spec.Root, h)); err == nil {
			a = append(a, "--tmpfs", filepath.Join("/workspace", h))
		}
	}
	if d.CacheDir != "" {
		a = append(a, "--bind", d.CacheDir, "/cache")
	}
	if d.SocketDir != "" {
		a = append(a, "--ro-bind", d.SocketDir, SandboxSocketDir)
	}
	if bridgeDir != "" {
		a = append(a, "--bind", bridgeDir, "/run/organism")
	}

	env := map[string]string{
		"PATH":                       os.Getenv("PATH"),
		"HOME":                       "/tmp/home",
		"LANG":                       "C.UTF-8",
		"GOPATH":                     "/cache/go",
		"GOMODCACHE":                 "/cache/go/pkg/mod",
		"GOCACHE":                    "/cache/go-build",
		"GOFLAGS":                    "-buildvcs=false -modcacherw",
		"GOTOOLCHAIN":                "local",
		"CGO_ENABLED":                "0",
		"npm_config_cache":           "/cache/npm",
		"npm_config_update_notifier": "false",
		"CI":                         "true",
	}
	for k, v := range base {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	a = append(a, "--clearenv")
	for _, k := range keys {
		a = append(a, "--setenv", k, env[k])
	}
	a = append(a, "--chdir", "/workspace", "bash", "-c", line)
	return a
}

// BwrapAvailable reports whether bubblewrap can create sandboxes here.
func BwrapAvailable() bool {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return false
	}
	return exec.Command("bwrap", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--unshare-pid", "true").Run() == nil
}

// systemDirs are the top-level directories a sandbox may see (read-only).
var systemDirs = map[string]bool{
	"usr": true, "bin": true, "sbin": true, "lib": true, "lib32": true, "lib64": true, "libx32": true,
	"etc": true, "opt": true, "var": true, "sys": true, "nix": true,
}

func systemMounts() []string {
	var a []string
	entries, err := os.ReadDir("/")
	if err != nil {
		return []string{"--ro-bind", "/usr", "/usr"}
	}
	for _, e := range entries {
		name := e.Name()
		if !systemDirs[name] {
			continue
		}
		p := "/" + name
		if e.Type()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(p); err == nil {
				a = append(a, "--symlink", target, p)
			}
			continue
		}
		a = append(a, "--ro-bind", p, p)
	}
	return a
}
