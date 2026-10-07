package infra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	PostgresContainer = "seed-postgres"
	PostgresImage     = "postgres:17"
	PostgresHostPort  = "55432"
)

// Docker runs docker CLI commands.
func Docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// DockerAvailable reports whether the docker daemon is reachable.
func DockerAvailable(ctx context.Context) bool {
	_, err := Docker(ctx, "info", "--format", "{{.ServerVersion}}")
	return err == nil
}

// EnsureNetwork creates a bridge network if missing.
func EnsureNetwork(ctx context.Context, name string) error {
	if _, err := Docker(ctx, "network", "inspect", name); err == nil {
		return nil
	}
	_, err := Docker(ctx, "network", "create", name)
	return err
}

// EnsurePostgres starts the shared Seed PostgreSQL container on network and
// waits until admin can connect.
func EnsurePostgres(ctx context.Context, network string, admin Admin) error {
	if admin.Ping(ctx) == nil {
		return nil
	}
	if err := EnsureNetwork(ctx, network); err != nil {
		return err
	}
	state, err := Docker(ctx, "inspect", "-f", "{{.State.Running}}", PostgresContainer)
	switch {
	case err != nil:
		slog.Info("starting PostgreSQL container", "name", PostgresContainer, "port", PostgresHostPort)
		_, err = Docker(ctx, "run", "-d", "--name", PostgresContainer, "--restart", "unless-stopped",
			"--network", network, "-p", "127.0.0.1:"+PostgresHostPort+":5432",
			"-e", "POSTGRES_USER=seed", "-e", "POSTGRES_PASSWORD=seed", "-e", "POSTGRES_DB=postgres",
			"-v", "seed-postgres-data:/var/lib/postgresql/data", PostgresImage)
		if err != nil {
			return err
		}
	case state != "true":
		if _, err := Docker(ctx, "start", PostgresContainer); err != nil {
			return err
		}
	}
	// Make sure it is attached to the sandbox network (compose-created containers may not be).
	_, _ = Docker(ctx, "network", "connect", network, PostgresContainer)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if err := admin.Ping(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("PostgreSQL did not become ready at %s", PostgresHostPort)
}

// EnsureImage builds the sandbox image from dockerfile (if not already built)
// and returns the image reference, tagged by content hash so changes rebuild.
func EnsureImage(ctx context.Context, base, dockerfile string) (string, error) {
	content, err := os.ReadFile(dockerfile)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	ref := base + ":" + hex.EncodeToString(sum[:])[:12]
	if _, err := Docker(ctx, "image", "inspect", ref); err == nil {
		return ref, nil
	}
	slog.Info("building sandbox image (first run takes a minute)", "image", ref)
	// The Dockerfile is sent on stdin with no build context: the image must not
	// depend on (or leak) the Seed's working tree.
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", ref, "-")
	cmd.Stdin = bytes.NewReader(content)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		tail := out.String()
		if len(tail) > 3000 {
			tail = tail[len(tail)-3000:]
		}
		return "", fmt.Errorf("docker build: %w\n%s", err, tail)
	}
	return ref, nil
}
