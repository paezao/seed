package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSecretFlags(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-from-env")
	env := filepath.Join(t.TempDir(), "seed.env")
	os.WriteFile(env, []byte("# my secrets\nexport GITHUB_TOKEN=\"ghp_x\"\nSTRIPE_KEY='sk_test_1'\n\n"), 0o600)
	f, err := parseRunFlags("run", []string{"-e", "OPENROUTER_API_KEY", "-e", "SEED_MODEL=minimax/minimax-m3", "--env-file", env, "--dir", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"OPENROUTER_API_KEY": "sk-or-from-env", "SEED_MODEL": "minimax/minimax-m3", "GITHUB_TOKEN": "ghp_x", "STRIPE_KEY": "sk_test_1"}
	for k, v := range want {
		if f.secrets[k] != v {
			t.Errorf("%s = %q, want %q", k, f.secrets[k], v)
		}
	}
	if strings.Join(f.secretNames(), ",") != "GITHUB_TOKEN,OPENROUTER_API_KEY,SEED_MODEL,STRIPE_KEY" {
		t.Errorf("names: %v", f.secretNames())
	}
	if _, err := parseRunFlags("run", []string{"-e", "NOT_SET_ANYWHERE_XYZ"}); err == nil {
		t.Error("-e of an unset variable should fail loudly")
	}
	if _, err := parseRunFlags("run", []string{"-e", "bad-name=x"}); err == nil {
		t.Error("invalid names should be rejected")
	}
}

func TestLoopbackAddrOnly(t *testing.T) {
	for in, want := range map[string]bool{
		"127.0.0.1:8081": true, "localhost:8081": true, "[::1]:8081": true,
		"169.254.169.254:80": false, "10.0.0.5:5432": false, "example.com:443": false,
		"0.0.0.0:8080": false, "[::]:8080": false, "127.0.0.1:0": false, "127.0.0.1": false,
	} {
		if _, ok := loopbackAddr(in); ok != want {
			t.Errorf("loopbackAddr(%q) = %v, want %v", in, ok, want)
		}
	}
}

func TestDeployDockerfileIsGenerated(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	base, plant, tail, got := read("Dockerfile"), read("deploy/plant.Dockerfile"), read("deploy/tail.Dockerfile"), read("Dockerfile.deploy")
	var want strings.Builder
	want.WriteString("# GENERATED from Dockerfile and deploy/*.Dockerfile by `make deploy-dockerfile`: do not edit.\n")
	for _, line := range strings.SplitAfter(base, "\n") {
		want.WriteString(line)
		if regexp.MustCompile(`^FROM golang:.* AS go\n$`).MatchString(line) {
			want.WriteString(plant)
		}
	}
	want.WriteString(tail)
	if got != want.String() {
		t.Fatal("Dockerfile.deploy is out of date: run `make deploy-dockerfile`")
	}
}

func TestPlantRefusesNonEmpty(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "important.txt"), []byte("x"), 0o644)
	if err := cmdPlant(context.Background(), []string{"--into", dir}); err == nil {
		t.Fatal("must not plant over someone else's files")
	}
	seed := t.TempDir()
	os.WriteFile(filepath.Join(seed, "seed.yaml"), []byte("name: x\n"), 0o644)
	if err := cmdPlant(context.Background(), []string{"--into", seed}); err != nil {
		t.Fatal("an existing Seed is left alone")
	}
}

func TestKeyHint(t *testing.T) {
	if h := keyHint([]string{"SEED_TEST_OPENROUTER_API_KEY"}); !strings.Contains(h, "-e OPENROUTER_API_KEY=$SEED_TEST_OPENROUTER_API_KEY") {
		t.Fatalf("hint: %q", h)
	}
	if h := keyHint([]string{"OPENROUTER_API_KEY", "STRIPE_KEY"}); h != "" {
		t.Fatalf("no hint when the key is passed right: %q", h)
	}
	if h := keyHint(nil); !strings.Contains(h, "without a brain") {
		t.Fatalf("no key at all: %q", h)
	}
}
