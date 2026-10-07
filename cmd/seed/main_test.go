package main

import (
	"os"
	"path/filepath"
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
