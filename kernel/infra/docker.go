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

// EnsureImage builds the sandbox image from dockerfile (if not already built)
// and returns the image reference, tagged by content hash so changes rebuild.
func EnsureImage(ctx context.Context, base, dockerfile string) (string, error) {
	content, err := os.ReadFile(dockerfile)
	if err != nil {
		return "", err
	}
	return EnsureImageFrom(ctx, base, content)
}

// EnsureImageFrom is EnsureImage for a Dockerfile's content.
func EnsureImageFrom(ctx context.Context, base string, content []byte) (string, error) {
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
