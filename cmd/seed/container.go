package main

import (
	"context"
	_ "embed"
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
	"seed/kernel/git"
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

func hostAddrPath(root string) string { return filepath.Join(root, ".seed", "host-addr") }

// containerName is unique per Seed (its name plus the hash of its first commit).
func containerName(ctx context.Context, cfg *config.Config) string {
	inst := ""
	if out, err := git.Open(cfg.Root).Run(ctx, "rev-list", "--max-parents=0", "HEAD"); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if l := lines[len(lines)-1]; len(l) >= 8 {
			inst = "-" + l[:8]
		}
	}
	return "seed-" + cfg.Name + inst
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
		addr := readTrim(hostAddrPath(cfg.Root))
		fmt.Fprintf(os.Stderr, "I'm already running at http://%s/_seed/\n", localhost(addr))
		if f.open {
			openWhenUp(ctx, func() string { return addr })
		}
		if f.detach {
			return nil
		}
		return followContainer(ctx, name)
	}
	_, _ = engine(ctx, "rm", "-f", name)
	kernelHint(cfg.Root)

	fmt.Fprintln(os.Stderr, "preparing my body (the first time builds the runtime image; it takes a few minutes)…")
	image, err := infra.EnsureImage(ctx, runtimeImage, filepath.Join(cfg.Root, "Dockerfile"))
	if err != nil {
		return err
	}
	hostPort, err := pickPort(f.addr)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cfg.Root, ".seed"), 0o755); err != nil {
		return err
	}
	seccompPath, err := writeSeccomp()
	if err != nil {
		return err
	}
	run := []string{"run", "-d", "--rm", "--name", name,
		"--label", "seed.root=" + cfg.Root,
		"-p", "127.0.0.1:" + strconv.Itoa(hostPort) + ":8080",
		"-v", cfg.Root + ":/seed",
		// The owner's secrets: names on the command line, values through the
		// engine's environment (kept out of `ps`); the kernel keeps them in memory.
		"-e", "SEED_SECRETS=" + strings.Join(f.secretNames(), ","),
		// Hardened, unprivileged container: no capabilities, no new
		// privileges, a read-only root filesystem, resource limits, and
		// Docker's default seccomp profile extended only with the namespace
		// and mount calls nested bubblewrap sandboxes need (cmd/seed/seccomp.json).
		// AppArmor's docker-default profile and /proc masking also forbid
		// those, so they are relaxed.
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--security-opt", "seccomp=" + seccompPath,
		"--security-opt", "apparmor=unconfined", "--security-opt", "systempaths=unconfined",
		"--read-only", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g",
		"--pids-limit", envOr("SEED_PIDS_LIMIT", "4096"),
		"--memory", envOr("SEED_MEMORY", "8g"),
		"--cpus", envOr("SEED_CPUS", strconv.Itoa(max(1, goruntime.NumCPU()/2))),
	}
	if goruntime.GOOS == "linux" {
		// Files the Seed writes stay owned by you.
		run = append(run, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()))
	}
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
	if err := os.WriteFile(hostAddrPath(cfg.Root), []byte(addr), 0o644); err != nil {
		return err
	}
	if f.open {
		go openWhenUp(ctx, func() string { return addr })
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

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
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

// cmdUpgrade replaces a stopped Seed's kernel with this CLI's, as a new generation.
func cmdUpgrade(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Seed directory")
	force := fs.Bool("force", false, "upgrade even if the Seed changed its own kernel (those changes are replaced)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	if state, err := engine(ctx, "inspect", "-f", "{{.State.Running}}", containerName(ctx, cfg)); err == nil && state == "true" {
		return errors.New("I'm running: stop me first (`seed stop`), then upgrade")
	}
	res, err := template.Upgrade(ctx, cfg.Root, *force)
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
