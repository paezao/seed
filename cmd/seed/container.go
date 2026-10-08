package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"seed/kernel/config"
	"seed/kernel/fsx"
	"seed/kernel/infra"
	"seed/kernel/template"
)

// A Seed runs in one container: its kernel, its private PostgreSQL, its
// toolchains and bubblewrap for its sandboxes, with the Seed's folder mounted
// at /seed. The folder is the Seed; the container is just its body, and the
// same image is what you deploy.

const runtimeImage = "seed-runtime"

// containerEngine is docker, or podman if SEED_CONTAINER_ENGINE says so.
func containerEngine() string {
	if e := os.Getenv("SEED_CONTAINER_ENGINE"); e != "" {
		return e
	}
	return "docker"
}

// containerName is unique per Seed folder. It is derived from the folder's
// path, not from git: the CLI never runs git in an existing Seed's folder on
// the host (its .git/config is under the Seed's control).
func containerName(_ context.Context, cfg *config.Config) string {
	sum := sha256.Sum256([]byte(cfg.Root))
	return "seed-" + cfg.Name + "-" + hex.EncodeToString(sum[:4])
}

func engine(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, containerEngine(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%s %s: %w: %s", containerEngine(), args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// cmdRun starts the Seed in its container and follows its log.
func cmdRun(ctx context.Context, args []string) error {
	f, err := parseRunFlags("run", args)
	if err != nil {
		return err
	}
	if f.native {
		return runNative(ctx, f)
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	if _, err := engine(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		return fmt.Errorf("I run in a container: install Docker (or Podman, with SEED_CONTAINER_ENGINE=podman) and make sure it is running (%v)", err)
	}
	name := containerName(ctx, cfg)

	if state, err := engine(ctx, "inspect", "-f", "{{.State.Running}}", name); err == nil && state == "true" {
		addr, err := publishedAddr(ctx, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "I'm already running at http://%s/_seed/\n", localhost(addr))
		if f.open {
			openWhenUp(ctx, cfg.Root, func() string { return addr })
		}
		if f.detach {
			return nil
		}
		return followContainer(ctx, name)
	}
	_, _ = engine(ctx, "rm", "-f", name)
	kernelHint(cfg.Root)
	if hint := keyHint(f.secretNames()); hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}

	fmt.Fprintln(os.Stderr, "preparing my body (the first time builds the runtime image; it takes a few minutes)…")
	dockerfile, err := fsx.ReadFile(cfg.Root, "Dockerfile") // confined to the Seed's folder
	if err != nil {
		return err
	}
	image, err := infra.EnsureImageFrom(ctx, runtimeImage, dockerfile)
	if err != nil {
		return err
	}
	hostPort, err := pickPort(f.addr)
	if err != nil {
		return err
	}
	seccompPath, err := writeSeccomp()
	if err != nil {
		return err
	}
	run := []string{"run", "-d", "--rm", "--name", name,
		"--label", "seed.kind=body",
		"--label", "seed.root=" + cfg.Root,
		"-p", "127.0.0.1:" + strconv.Itoa(hostPort) + ":8080",
		"-v", cfg.Root + ":/seed",
		// The owner's secrets: names on the command line, values through the
		// engine's environment (kept out of `ps`); the kernel keeps them in memory.
		"-e", "SEED_SECRETS=" + strings.Join(f.secretNames(), ","),
		// Hardened, unprivileged container (see hardening()).
	}
	run = append(run, hardening(seccompPath)...)
	run = append(run, "--cpus", envOr("SEED_CPUS", strconv.Itoa(max(1, goruntime.NumCPU()/2))))
	run = append(run, userArgs()...)
	for _, n := range f.secretNames() {
		run = append(run, "-e", n)
	}
	run = append(run, image)
	start := exec.CommandContext(ctx, containerEngine(), run...)
	start.Env = append(os.Environ(), f.secretEnv()...)
	if out, err := start.CombinedOutput(); err != nil {
		return fmt.Errorf("%s run: %w: %s", containerEngine(), err, strings.TrimSpace(string(out)))
	}
	addr := "127.0.0.1:" + strconv.Itoa(hostPort)
	if f.open {
		go openWhenUp(ctx, cfg.Root, func() string { return addr })
	}
	if f.detach {
		fmt.Fprintf(os.Stderr, "started %s; talk to me at http://%s/_seed/ (stop with `seed stop`)\n", name, localhost(addr))
		return nil
	}
	err = followContainer(ctx, name)
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\nresting…")
		_, _ = engine(context.Background(), "stop", "-t", "30", name)
		return nil
	}
	return err
}

// follow streams the container's log until it exits or ctx ends.
func followContainer(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, containerEngine(), "logs", "-f", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func cmdStop(ctx context.Context, args []string) error {
	f, err := parseRunFlags("stop", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	name := containerName(ctx, cfg)
	if _, err := engine(ctx, "stop", "-t", "30", name); err != nil {
		return fmt.Errorf("I'm not running (%v)", err)
	}
	fmt.Fprintln(os.Stderr, "resting.")
	return nil
}

// pickPort honors an explicit address, otherwise the first free port from 8080.
func pickPort(addr string) (int, error) {
	if addr != "" {
		_, p, err := net.SplitHostPort(addr)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(p)
	}
	for p := 8080; p < 8130; p++ {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			l.Close()
			return p, nil
		}
	}
	return 0, errors.New("no free port between 8080 and 8129")
}

func localhost(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return "localhost:" + p
	}
	return addr
}

// readSeedFile reads a file in a Seed's folder without following symlinks out of it.
func readSeedFile(root, rel string) string {
	b, _ := fsx.ReadFile(root, rel)
	if len(b) > 4096 {
		b = b[:4096]
	}
	return strings.TrimSpace(string(b))
}

// cmdImage builds (if needed) and prints the runtime image for the Seed in
// --dir (development and CI use it to run tests in the same body).
func cmdImage(ctx context.Context, args []string) error {
	f, err := parseRunFlags("image", args)
	if err != nil {
		return err
	}
	image, err := infra.EnsureImage(ctx, runtimeImage, filepath.Join(f.dir, "Dockerfile"))
	if err != nil {
		return err
	}
	fmt.Println(image)
	return nil
}

//go:embed seccomp.json
var seccompProfile []byte

// writeSeccomp stores the embedded seccomp profile where the engine can read
// it (derived from moby/profiles' default.json, Apache-2.0).
func writeSeccomp() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "seed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "seccomp.json")
	return p, os.WriteFile(p, seccompProfile, 0o644)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// cmdUpgrade replaces a stopped Seed's kernel with this CLI's, as a new
// generation. The work (git in the Seed's folder, writes into it) happens in
// a throwaway hardened Seed container, never on the host: the folder's git
// config and symlinks are under the Seed's control.
func cmdUpgrade(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Seed directory")
	force := fs.Bool("force", false, "upgrade even if the Seed changed its own kernel (those changes are replaced)")
	inside := fs.Bool("inside", false, "internal: perform the upgrade (inside the container)")
	tmpl := fs.String("template", "", "internal: template archive to upgrade to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inside {
		return upgradeInside(ctx, *dir, *tmpl, *force)
	}
	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	if state, err := engine(ctx, "inspect", "-f", "{{.State.Running}}", containerName(ctx, cfg)); err == nil && state == "true" {
		return errors.New("I'm running: stop me first (`seed stop`), then upgrade")
	}
	archive, err := template.Archive()
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "seed-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "template.tar.gz"), archive, 0o644); err != nil {
		return err
	}
	df, err := template.File(archive, "Dockerfile")
	if err != nil {
		return err
	}
	image, err := infra.EnsureImageFrom(ctx, runtimeImage, df)
	if err != nil {
		return err
	}
	seccompPath, err := writeSeccomp()
	if err != nil {
		return err
	}
	// Build the new kernel's own tool from the template inside the container
	// (works whatever the host OS) and let it upgrade /seed.
	script := `set -e; mkdir -p /tmp/k /tmp/home; tar -xzf /upgrade/template.tar.gz -C /tmp/k; cd /tmp/k
export GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomod GOWORK=off GOFLAGS="-mod=readonly -buildvcs=false -modcacherw"
echo "building the new kernel's upgrade tool…"
go build -o /tmp/seed-new ./cmd/seed
exec /tmp/seed-new upgrade --inside --template /upgrade/template.tar.gz --dir /seed "$@"`
	run := []string{"run", "--rm", "-v", cfg.Root + ":/seed", "-v", stage + ":/upgrade:ro",
		"--memory", envOr("SEED_MEMORY", "8g"), "--entrypoint", "/bin/sh"}
	run = append(run, hardening(seccompPath)...)
	run = append(run, userArgs()...)
	run = append(run, image, "-c", script, "upgrade")
	if *force {
		run = append(run, "--force")
	}
	cmd := exec.CommandContext(ctx, containerEngine(), run...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}
	return nil
}

// upgradeInside runs in the throwaway container.
func upgradeInside(ctx context.Context, dir, tmpl string, force bool) error {
	archive, err := os.ReadFile(tmpl)
	if err != nil {
		return err
	}
	res, err := template.Upgrade(ctx, dir, force, archive)
	if err != nil {
		return err
	}
	if res.UpToDate {
		fmt.Printf("My kernel is already %s.\n", res.To)
		return nil
	}
	fmt.Printf("Upgraded my kernel %s → %s: generation %d (%s).\n", res.From, res.To, res.Generation, shortHash(res.Commit))
	fmt.Printf("  %d file(s) changed, %d removed", len(res.Changed), len(res.Removed))
	if len(res.NewSkills) > 0 {
		fmt.Printf(", new starter skills: %s", strings.Join(res.NewSkills, ", "))
	}
	fmt.Println(".\n  My organism, knowledge, data and skills are unchanged.")
	fmt.Println("\nStart me again with: seed run -e OPENROUTER_API_KEY")
	return nil
}

// hardening is the container security posture shared by a Seed's body and
// its upgrade container: no capabilities, no new privileges, read-only root,
// tailored seccomp (Docker default + the calls nested bubblewrap needs).
func hardening(seccompPath string) []string {
	return []string{
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--security-opt", "seccomp=" + seccompPath,
		"--security-opt", "apparmor=unconfined", "--security-opt", "systempaths=unconfined",
		"--read-only", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g",
		"--pids-limit", envOr("SEED_PIDS_LIMIT", "4096"),
		"--memory", envOr("SEED_MEMORY", "8g"),
	}
}

// userArgs runs containers as you on Linux (files stay yours).
func userArgs() []string {
	if goruntime.GOOS == "linux" {
		return []string{"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
	}
	return nil
}

// kernelHint tells the owner when this CLI carries a newer kernel.
func kernelHint(root string) {
	if !template.Available() {
		return
	}
	latest, err := template.Version()
	if err != nil {
		return
	}
	if mine := template.SeedVersion(root); mine != latest {
		fmt.Fprintf(os.Stderr, "A newer kernel is available (%s → %s): run `seed upgrade` while I'm stopped.\n", mine, latest)
	}
}

// publishedAddr asks the container engine where a Seed's port is published.
// Addresses are never taken from the Seed's folder: the Seed controls it, and
// could otherwise point the CLI at any host (SSRF). Only loopback is accepted.
func publishedAddr(ctx context.Context, name string) (string, error) {
	out, err := engine(ctx, "port", name, "8080/tcp")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if a, ok := loopbackAddr(strings.TrimSpace(line)); ok {
			return a, nil
		}
	}
	return "", fmt.Errorf("%s publishes no loopback port", name)
}

// loopbackAddr accepts only host:port on this machine's loopback interface.
func loopbackAddr(s string) (string, bool) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", false
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", false
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	return net.JoinHostPort(ip.String(), port), true
}
