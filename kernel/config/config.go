// Package config loads seed.yaml, the Seed's operating configuration.
//
// seed.yaml is part of the kernel trust boundary: the organism cannot change
// it without a kernel-level (dangerous) permission.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"seed/kernel/fsx"
)

type Config struct {
	Name        string            `yaml:"name" json:"name"`
	Server      ServerConfig      `yaml:"server" json:"server"`
	Model       ModelConfig       `yaml:"model" json:"model"`
	Database    DatabaseConfig    `yaml:"database" json:"database"`
	Sandbox     SandboxConfig     `yaml:"sandbox" json:"sandbox"`
	Organism    OrganismConfig    `yaml:"organism" json:"organism"`
	Evolution   EvolutionConfig   `yaml:"evolution" json:"evolution"`
	Permissions PermissionsConfig `yaml:"permissions" json:"permissions"`
	Kernel      KernelConfig      `yaml:"kernel" json:"kernel"`

	// Root is the absolute path of the Seed's repository (not serialized).
	Root string `yaml:"-" json:"root"`
	// Instance distinguishes Seeds that share a name (the short hash of the
	// repository's root commit). It scopes databases and containers so a new
	// Seed never inherits another's memory. Set at boot.
	Instance string `yaml:"-" json:"instance"`
}

type ServerConfig struct {
	Addr string `yaml:"addr" json:"addr"`
	// AllowedHosts are extra Host header values accepted (besides localhost,
	// 127.0.0.1, [::1] and *.localhost). Requests for any other host are
	// refused, which defeats DNS-rebinding attacks from other websites.
	AllowedHosts []string `yaml:"allowed_hosts" json:"allowed_hosts"`
}

type ModelConfig struct {
	// The model itself (provider, model, key) is chosen by the owner in the
	// control plane and stored outside the repository; see runtime/mind.go.
	MaxTokens int `yaml:"max_tokens" json:"max_tokens"`
}

type DatabaseConfig struct {
	// URL is an administrative connection to the PostgreSQL server used by the
	// kernel. It must be able to create databases and roles. Empty means the
	// Seed's own private PostgreSQL (kernel/pg).
	URL string `yaml:"url" json:"-"`
}

type SandboxConfig struct {
	Driver string `yaml:"driver" json:"driver"` // bwrap | local
	// DBHost is how sandboxed code reaches PostgreSQL: the socket directory
	// as mounted in the sandbox.
	DBHost string `yaml:"db_host" json:"db_host"`
}

type OrganismConfig struct {
	Build  string `yaml:"build" json:"build"`
	Test   string `yaml:"test" json:"test"`
	Run    string `yaml:"run" json:"run"`
	Port   int    `yaml:"port" json:"port"`
	Health string `yaml:"health" json:"health"`
	// Migrations is the directory (relative to Root) holding organism SQL migrations.
	Migrations string `yaml:"migrations" json:"migrations"`
}

type EvolutionConfig struct {
	MaxRepairAttempts int `yaml:"max_repair_attempts" json:"max_repair_attempts"`
	MaxAgentTurns     int `yaml:"max_agent_turns" json:"max_agent_turns"`
	// CommandTimeoutSeconds bounds a single sandbox command.
	CommandTimeoutSeconds int `yaml:"command_timeout_seconds" json:"command_timeout_seconds"`
}

type PermissionsConfig struct {
	Safe                 string `yaml:"safe" json:"safe"`
	Review               string `yaml:"review" json:"review"`
	Dangerous            string `yaml:"dangerous" json:"dangerous"`
	RequirePlanApproval  bool   `yaml:"require_plan_approval" json:"require_plan_approval"`
	RequireApplyApproval bool   `yaml:"require_apply_approval" json:"require_apply_approval"`
}

type KernelConfig struct {
	// Evolvable lists the directories (relative to Root, trailing slash) the
	// Seed may change freely. Everything else is the kernel: changing it is a
	// dangerous, owner-approved action, and sandboxes see it read-only. An
	// allowlist, so that new files the kernel would act on (go.work, vendor/,
	// ...) are protected by default.
	Evolvable []string `yaml:"evolvable" json:"evolvable"`
}

// Defaults returns the configuration used for any field seed.yaml leaves empty.
func Defaults() Config {
	return Config{
		Name:   "seed",
		Server: ServerConfig{Addr: "127.0.0.1:8080"},
		Model:  ModelConfig{MaxTokens: 16000},
		// Database.URL empty: use the managed local server (seed infra).
		Sandbox: SandboxConfig{Driver: "bwrap", DBHost: "/run/seed-db"},
		Organism: OrganismConfig{
			Build: "make -C organism build", Test: "make -C organism test",
			Run: "organism/bin/server", Port: 8080, Health: "/healthz",
			Migrations: "organism/migrations",
		},
		Evolution: EvolutionConfig{MaxRepairAttempts: 3, MaxAgentTurns: 150, CommandTimeoutSeconds: 600},
		Permissions: PermissionsConfig{
			Safe: "allow", Review: "allow", Dangerous: "ask",
		},
		Kernel: KernelConfig{Evolvable: []string{"organism/", "knowledge/", "skills/"}},
	}
}

// Load reads <root>/seed.yaml over the defaults and applies environment overrides.
func Load(root string) (*Config, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	c := Defaults()
	// Confined to the folder: a symlinked seed.yaml cannot make the host CLI
	// read files outside the Seed.
	data, err := fsx.ReadFile(abs, "seed.yaml")
	if err != nil {
		return nil, fmt.Errorf("read seed.yaml: %w (is this a Seed directory?)", err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse seed.yaml: %w", err)
	}
	c.Root = abs
	c.applyEnv()
	return &c, c.Validate()
}

func (c *Config) applyEnv() {
	if v := os.Getenv("SEED_DATABASE_URL"); v != "" {
		c.Database.URL = v
	}
	if v := os.Getenv("SEED_ADDR"); v != "" {
		c.Server.Addr = v
	}
	if v := os.Getenv("SEED_SANDBOX_DRIVER"); v != "" {
		c.Sandbox.Driver = v
	}
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

func (c *Config) Validate() error {
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("seed.yaml: name %q must match %s", c.Name, nameRe)
	}
	for _, lvl := range []string{c.Permissions.Safe, c.Permissions.Review, c.Permissions.Dangerous} {
		switch lvl {
		case "allow", "ask", "deny":
		default:
			return fmt.Errorf("seed.yaml: permission decision %q must be allow, ask or deny", lvl)
		}
	}
	switch c.Sandbox.Driver {
	case "bwrap", "local":
	default:
		return fmt.Errorf("seed.yaml: sandbox.driver %q must be bwrap or local", c.Sandbox.Driver)
	}
	return nil
}

// DBName returns a PostgreSQL-safe database (or role) name for this Seed
// instance with the given suffix.
func (c *Config) DBName(suffix string) string {
	base := strings.ReplaceAll(c.Name, "-", "_")
	if c.Instance != "" {
		base += "_" + c.Instance
	}
	return base + "_" + suffix
}

// ContainerName returns a Docker container name for this Seed instance.
func (c *Config) ContainerName(suffix string) string {
	base := "seed-" + c.Name
	if c.Instance != "" {
		base += "-" + c.Instance
	}
	return base + "-" + suffix
}
