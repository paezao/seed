package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"seed/kernel/git"
	"seed/kernel/migrate"
	"seed/kernel/permissions"
	"seed/kernel/sandbox"
	"seed/kernel/skills"
)

// OrganismMigrationsTable records applied organism migrations.
const OrganismMigrationsTable = "schema_migrations"

// Env is what the sandbox-facing tools operate on during an evolution.
type Env struct {
	Sandbox sandbox.Sandbox
	// Root is the host path of the workspace (the evolution worktree).
	Root string
	// DBURL is the host-side URL of the evolution's scratch database.
	DBURL string
	// SandboxEnv is passed to every sandbox command (DATABASE_URL etc.).
	SandboxEnv map[string]string
	// MigrationsDir is relative to Root.
	MigrationsDir string
	// RunCommand starts the organism; HealthPath is polled to see it is up.
	RunCommand     string
	HealthPath     string
	CommandTimeout time.Duration
	// MaxCommandTimeout caps per-call timeouts requested by the agent.
	MaxCommandTimeout time.Duration
}

const appProcess = "organism"

// ExecTools runs commands and the organism inside the sandbox.
func ExecTools(env *Env) []*Tool {
	return []*Tool{
		{
			Name: "run",
			Description: "Run a shell command (bash) inside the evolution sandbox, in the workspace root (/workspace). " +
				"Use it to build, test, install dependencies, and inspect. The sandbox has go, node/npm, make, psql, curl and jq; " +
				"DATABASE_URL points at this evolution's scratch database. Output is combined stdout+stderr.",
			Schema: Schema(Props{
				"command":         Str("bash command line"),
				"timeout_seconds": Int("timeout in seconds (default 300)"),
			}, "command"),
			Classify: func(in json.RawMessage) (permissions.Level, string) {
				var a struct{ Command string }
				_ = json.Unmarshal(in, &a)
				return permissions.Review, "run `" + Truncate(a.Command, 200) + "` in sandbox"
			},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct {
					Command        string
					TimeoutSeconds int `json:"timeout_seconds"`
				}
				if err := decode(in, &a); err != nil {
					return "", err
				}
				timeout := env.CommandTimeout
				if timeout == 0 {
					timeout = 5 * time.Minute
				}
				if a.TimeoutSeconds > 0 {
					timeout = time.Duration(a.TimeoutSeconds) * time.Second
				}
				if env.MaxCommandTimeout > 0 && timeout > env.MaxCommandTimeout {
					timeout = env.MaxCommandTimeout
				}
				res, err := env.Sandbox.Exec(ctx, a.Command, timeout, env.SandboxEnv)
				if err != nil {
					return "", err
				}
				return FormatExec(res), nil
			},
		},
		{
			Name: "start_app",
			Description: "Build nothing; just (re)start the organism server inside the sandbox using the configured run command, " +
				"wait for its health endpoint, and report status with recent logs. Build first with `run`.",
			Schema:   Schema(Props{}),
			Classify: Fixed(permissions.Review, "start organism in sandbox"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				return StartApp(ctx, env, 45*time.Second)
			},
		},
		{
			Name:        "stop_app",
			Description: "Stop the organism server running in the sandbox.",
			Schema:      Schema(Props{}),
			Classify:    Fixed(permissions.Safe, "stop organism in sandbox"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				return "stopped", env.Sandbox.Stop(ctx, appProcess)
			},
		},
		{
			Name:        "app_logs",
			Description: "Read the organism server's recent log output in the sandbox.",
			Schema:      Schema(Props{"lines": Int("number of lines (default 100)")}),
			Classify:    Fixed(permissions.Safe, "read organism logs"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Lines int }
				_ = decode(in, &a)
				if a.Lines <= 0 {
					a.Lines = 100
				}
				state := "running"
				if !env.Sandbox.Running(ctx, appProcess) {
					state = "not running"
				}
				return fmt.Sprintf("organism is %s\n%s", state, env.Sandbox.Logs(ctx, appProcess, a.Lines)), nil
			},
		},
		{
			Name:        "http_request",
			Description: "Send an HTTP request to the organism running in the sandbox (start it with start_app first). Returns status, content type and body.",
			Schema: Schema(Props{
				"method":  Enum("HTTP method", "GET", "POST", "PUT", "PATCH", "DELETE"),
				"path":    Str("path including query, e.g. /api/tasks?x=1"),
				"body":    Str("request body (JSON text for APIs)"),
				"headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "extra headers"},
			}, "method", "path"),
			Classify: Fixed(permissions.Safe, "http request to sandboxed organism"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct {
					Method, Path, Body string
					Headers            map[string]string
				}
				if err := decode(in, &a); err != nil {
					return "", err
				}
				status, ctype, body, err := HTTP(ctx, env.Sandbox, a.Method, a.Path, a.Body, a.Headers)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("HTTP %d\ncontent-type: %s\n\n%s", status, ctype, Truncate(body, 8000)), nil
			},
		},
	}
}

// FormatExec renders a command result for the model.
func FormatExec(res sandbox.ExecResult) string {
	head := fmt.Sprintf("exit code %d (%.1fs)", res.ExitCode, res.Duration.Seconds())
	if res.TimedOut {
		head = fmt.Sprintf("TIMED OUT after %.0fs", res.Duration.Seconds())
	}
	out := strings.TrimRight(res.Output, "\n")
	if out == "" {
		out = "(no output)"
	}
	return head + "\n" + Truncate(out, 15000)
}

// StartApp (re)starts the organism in the sandbox and waits for health.
func StartApp(ctx context.Context, env *Env, wait time.Duration) (string, error) {
	_ = env.Sandbox.Stop(ctx, appProcess)
	if err := env.Sandbox.Start(ctx, appProcess, env.RunCommand, env.SandboxEnv); err != nil {
		return "", err
	}
	deadline := time.Now().Add(wait)
	var lastErr string
	for time.Now().Before(deadline) {
		if !env.Sandbox.Running(ctx, appProcess) {
			return "", fmt.Errorf("organism exited during startup. logs:\n%s", env.Sandbox.Logs(ctx, appProcess, 60))
		}
		status, _, body, err := HTTP(ctx, env.Sandbox, "GET", env.HealthPath, "", nil)
		if err == nil && status == 200 {
			return fmt.Sprintf("organism running; GET %s -> 200 %s\nrecent logs:\n%s", env.HealthPath, Truncate(body, 300), env.Sandbox.Logs(ctx, appProcess, 20)), nil
		}
		if err != nil {
			lastErr = err.Error()
		} else {
			lastErr = fmt.Sprintf("HTTP %d: %s", status, Truncate(body, 300))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("organism did not become healthy within %s (last: %s). logs:\n%s", wait, lastErr, env.Sandbox.Logs(ctx, appProcess, 60))
}

// HTTP performs a request against the sandbox's published port.
func HTTP(ctx context.Context, sb sandbox.Sandbox, method, path, body string, headers map[string]string) (int, string, string, error) {
	base, err := sb.URL(ctx)
	if err != nil {
		return 0, "", "", err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if err != nil {
		return 0, "", "", err
	}
	if body != "" {
		req.Header.Set("content-type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header.Get("content-type"), string(b), nil
}

// DBTools operate on the evolution's scratch database.
func DBTools(env *Env) []*Tool {
	return []*Tool{
		{
			Name: "migrate",
			Description: "Apply pending SQL migrations from " + env.MigrationsDir + " to this evolution's scratch database. " +
				"Migrations are files named NNNN_description.sql; never edit a migration that exists in the current generation — add a new one.",
			Schema:   Schema(Props{}),
			Classify: Fixed(permissions.Review, "apply migrations to scratch database"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				res, err := migrate.ApplyURL(ctx, env.DBURL, OrganismMigrationsTable, filepath.Join(env.Root, env.MigrationsDir))
				if err != nil {
					return "", err
				}
				return res.String(), nil
			},
		},
		{
			Name:        "db_query",
			Description: "Run SQL against this evolution's scratch database (not the live one). Returns up to 100 rows.",
			Schema:      Schema(Props{"sql": Str("SQL statement")}, "sql"),
			Classify:    Fixed(permissions.Review, "query scratch database"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ SQL string }
				if err := decode(in, &a); err != nil {
					return "", err
				}
				return Query(ctx, env.DBURL, a.SQL, 100)
			},
		},
		{
			Name:        "db_schema",
			Description: "Describe tables and columns in this evolution's scratch database.",
			Schema:      Schema(Props{}),
			Classify:    Fixed(permissions.Safe, "inspect scratch database schema"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				return DescribeSchema(ctx, env.DBURL)
			},
		},
	}
}

// Query runs SQL and renders rows as a table.
func Query(ctx context.Context, url, sql string, maxRows int) (string, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var cols []string
	for _, f := range rows.FieldDescriptions() {
		cols = append(cols, f.Name)
	}
	var sb strings.Builder
	if len(cols) > 0 {
		sb.WriteString(strings.Join(cols, " | ") + "\n")
	}
	n := 0
	for rows.Next() {
		n++
		if n > maxRows {
			continue
		}
		vals, err := rows.Values()
		if err != nil {
			return "", err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = Truncate(fmt.Sprint(v), 200)
		}
		sb.WriteString(strings.Join(parts, " | ") + "\n")
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	tag := rows.CommandTag()
	if n > maxRows {
		fmt.Fprintf(&sb, "… %d rows total (showing %d)\n", n, maxRows)
	}
	fmt.Fprintf(&sb, "(%s)", tag.String())
	return sb.String(), nil
}

// DescribeSchema lists user tables with their columns.
func DescribeSchema(ctx context.Context, url string) (string, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `
		SELECT c.table_name, c.column_name, c.data_type, c.is_nullable, coalesce(c.column_default, '')
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	tables := map[string][]string{}
	var order []string
	for rows.Next() {
		var tbl, col, typ, nullable, def string
		if err := rows.Scan(&tbl, &col, &typ, &nullable, &def); err != nil {
			return "", err
		}
		if _, ok := tables[tbl]; !ok {
			order = append(order, tbl)
		}
		desc := col + " " + typ
		if nullable == "NO" {
			desc += " not null"
		}
		if def != "" {
			desc += " default " + def
		}
		tables[tbl] = append(tables[tbl], desc)
	}
	if len(order) == 0 {
		return "(no tables)", nil
	}
	sort.Strings(order)
	var sb strings.Builder
	for _, t := range order {
		fmt.Fprintf(&sb, "%s(\n  %s\n)\n", t, strings.Join(tables[t], ",\n  "))
	}
	return sb.String(), nil
}

// GitTools inspect the workspace's version history.
func GitTools(repo *git.Repo, base string) []*Tool {
	return []*Tool{
		{
			Name:        "git_status",
			Description: "Show files changed in this evolution's workspace (relative to the base generation).",
			Schema:      Schema(Props{}),
			Classify:    Fixed(permissions.Safe, "git status"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				s, err := repo.Status(ctx)
				if s == "" && err == nil {
					return "clean (no changes yet)", nil
				}
				return s, err
			},
		},
		{
			Name:        "git_diff",
			Description: "Show the diff of this evolution's changes so far (optionally for one path).",
			Schema:      Schema(Props{"path": Str("limit to this path"), "stat": Bool("only show a summary")}),
			Classify:    Fixed(permissions.Safe, "git diff"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct {
					Path string
					Stat bool
				}
				_ = decode(in, &a)
				_, _ = repo.Run(ctx, "add", "-A", "-N")
				args := []string{"HEAD"}
				if a.Stat {
					args = append([]string{"--stat"}, args...)
				}
				if a.Path != "" {
					args = append(args, "--", a.Path)
				}
				d, err := repo.Diff(ctx, args...)
				if d == "" && err == nil {
					return "(no changes)", nil
				}
				return d, err
			},
		},
		{
			Name:        "git_log",
			Description: "Show the generation history (commits) of this Seed.",
			Schema:      Schema(Props{"limit": Int("number of commits (default 20)")}),
			Classify:    Fixed(permissions.Safe, "git log"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Limit int }
				_ = decode(in, &a)
				if a.Limit <= 0 {
					a.Limit = 20
				}
				commits, err := repo.Log(ctx, base, a.Limit)
				if err != nil {
					return "", err
				}
				var sb strings.Builder
				for _, c := range commits {
					fmt.Fprintf(&sb, "%s %s %s\n", c.Hash[:10], c.Date[:10], c.Subject)
					if c.Body != "" {
						sb.WriteString("    " + strings.ReplaceAll(Truncate(c.Body, 600), "\n", "\n    ") + "\n")
					}
				}
				return sb.String(), nil
			},
		},
	}
}

// SkillTools give access to the skill library.
func SkillTools(lib skills.Library) []*Tool {
	return []*Tool{{
		Name:        "read_skill",
		Description: "Read a skill's instructions (SKILL.md) and list its supporting files. Read relevant skills before starting work.",
		Schema:      Schema(Props{"name": Str("skill name")}, "name"),
		Classify:    Fixed(permissions.Safe, "read skill"),
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var a struct{ Name string }
			if err := decode(in, &a); err != nil {
				return "", err
			}
			s, err := lib.Get(a.Name)
			if err != nil {
				names := []string{}
				for _, s := range lib.List() {
					names = append(names, s.Name)
				}
				return "", fmt.Errorf("%v (available: %s)", err, strings.Join(names, ", "))
			}
			return fmt.Sprintf("# skill: %s\n%s\n\nfiles:\n%s\n\n%s", s.Name, s.Description, strings.Join(s.Files, "\n"), s.Content), nil
		},
	}}
}

// FileExists is a small helper for callers composing environments.
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
