package runtime

import (
	"os"
	"strings"
	"sync"
)

// Secrets are the key/value pairs the owner passes when starting the Seed
// (`seed run -e OPENROUTER_API_KEY`, `--env-file`), or that a platform sets
// when the Seed is deployed. The Seed stores none of them: they live in the
// kernel's memory only. At boot they are read from the environment and then
// removed from it, so nothing the kernel starts (PostgreSQL, git, sandboxes)
// inherits them.
type Secrets struct {
	mu   sync.RWMutex
	vals map[string]string
}

// SecretsEnv names the variable listing which environment variables are
// owner secrets (set by the launcher: "OPENROUTER_API_KEY,GITHUB_TOKEN").
const SecretsEnv = "SEED_SECRETS"

// knownSecrets are always treated as secrets when present, so a platform can
// set them without SEED_SECRETS.
var knownSecrets = []string{"OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GITHUB_TOKEN",
	"SEED_OWNER_USER", "SEED_OWNER_PASSWORD"}

// LoadSecrets takes the owner's secrets out of the process environment.
func LoadSecrets() *Secrets {
	s := &Secrets{vals: map[string]string{}}
	names := append([]string{}, knownSecrets...)
	for _, n := range strings.Split(os.Getenv(SecretsEnv), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			if v != "" {
				s.vals[n] = v
			}
			_ = os.Unsetenv(n)
		}
	}
	_ = os.Unsetenv(SecretsEnv)
	return s
}

// Get returns a secret's value.
func (s *Secrets) Get(name string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vals[name]
}

// Names lists the secrets that were passed (never their values).
func (s *Secrets) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.vals))
	for k := range s.vals {
		out = append(out, k)
	}
	return out
}
