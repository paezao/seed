package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seed/kernel/models"
	"seed/kernel/permissions"
)

func ws(t *testing.T) *Workspace {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "organism"), 0o755)
	os.MkdirAll(filepath.Join(root, "kernel"), 0o755)
	os.WriteFile(filepath.Join(root, "organism", "a.txt"), []byte("one\ntwo\ntwo\n"), 0o644)
	return &Workspace{Root: root, Policy: permissions.NewPolicy("allow", "allow", "deny", []string{"organism/", "knowledge/", "skills/"})}
}

func TestResolveConfinement(t *testing.T) {
	w := ws(t)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(w.Root, "organism", "escape"))
	bad := []string{"../x", "organism/../../x", "/etc/passwd", ".git/config", "organism/escape/file", "organism/escape"}
	for _, p := range bad {
		if _, _, err := w.Resolve(p); err == nil {
			t.Errorf("Resolve(%q) should fail", p)
		}
	}
	good := map[string]string{"organism/a.txt": "organism/a.txt", "/workspace/organism/a.txt": "organism/a.txt", filepath.Join(w.Root, "organism/new.go"): "organism/new.go"}
	for in, want := range good {
		if _, rel, err := w.Resolve(in); err != nil || rel != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, rel, err, want)
		}
	}
}

func run(t *testing.T, r *Registry, name string, input any) Outcome {
	b, _ := json.Marshal(input)
	return r.Execute(context.Background(), models.ToolCall{ID: "x", Name: name, Input: b})
}

func TestKernelWritesAreDangerous(t *testing.T) {
	w := ws(t)
	r := NewRegistry(w.Policy, nil).Add(FileTools(w)...)
	o := run(t, r, "write_file", map[string]string{"path": "kernel/evil.go", "content": "x"})
	if !o.Denied || o.Level != permissions.Dangerous {
		t.Fatalf("kernel write should be denied as dangerous: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "kernel/evil.go")); err == nil {
		t.Fatal("file must not be written")
	}
	o = run(t, r, "write_file", map[string]string{"path": "organism/backend/main.go", "content": "package main\n"})
	if o.Result.IsError || o.Level != permissions.Review {
		t.Fatalf("organism write should succeed: %+v", o)
	}
	o = run(t, r, "delete_path", map[string]string{"path": "seed.yaml"})
	if !o.Denied {
		t.Fatalf("deleting seed.yaml should be denied: %+v", o)
	}
}

func TestEditFile(t *testing.T) {
	w := ws(t)
	r := NewRegistry(w.Policy, nil).Add(FileTools(w)...)
	if o := run(t, r, "edit_file", map[string]string{"path": "organism/a.txt", "old_string": "two", "new_string": "2"}); !o.Result.IsError || !strings.Contains(o.Result.Content, "matches 2 times") {
		t.Fatalf("ambiguous edit should fail: %+v", o.Result)
	}
	if o := run(t, r, "edit_file", map[string]any{"path": "organism/a.txt", "old_string": "two", "new_string": "2", "replace_all": true}); o.Result.IsError {
		t.Fatal(o.Result.Content)
	}
	b, _ := os.ReadFile(filepath.Join(w.Root, "organism/a.txt"))
	if string(b) != "one\n2\n2\n" {
		t.Fatalf("got %q", b)
	}
	if o := run(t, r, "read_file", map[string]string{"path": "organism/a.txt"}); !strings.Contains(o.Result.Content, "     1\tone") {
		t.Fatalf("read: %q", o.Result.Content)
	}
	if o := run(t, r, "search", map[string]string{"pattern": "^2$"}); !strings.Contains(o.Result.Content, "organism/a.txt:2") {
		t.Fatalf("search: %q", o.Result.Content)
	}
}

func TestWriteFilter(t *testing.T) {
	w := ws(t)
	w.WriteFilter = func(rel string) error {
		if !strings.HasPrefix(rel, "knowledge/") {
			return os.ErrPermission
		}
		return nil
	}
	r := NewRegistry(w.Policy, nil).Add(FileTools(w)...)
	if o := run(t, r, "write_file", map[string]string{"path": "organism/x", "content": "x"}); !o.Result.IsError {
		t.Fatal("write filter should refuse")
	}
	if o := run(t, r, "write_file", map[string]string{"path": "knowledge/x.md", "content": "x"}); o.Result.IsError {
		t.Fatal(o.Result.Content)
	}
}

func TestSymlinksPlantedBySandboxCannotEscape(t *testing.T) {
	w := ws(t)
	r := NewRegistry(w.Policy, nil).Add(FileTools(w)...)
	outside := filepath.Join(t.TempDir(), "bashrc")
	os.WriteFile(outside, []byte("original"), 0o644)
	os.Symlink(outside, filepath.Join(w.Root, "organism", "link"))
	if o := run(t, r, "write_file", map[string]string{"path": "organism/link", "content": "pwned"}); !o.Result.IsError {
		t.Fatal("writing through an escaping symlink must fail")
	}
	if o := run(t, r, "read_file", map[string]string{"path": "organism/link"}); !o.Result.IsError {
		t.Fatal("reading through an escaping symlink must fail")
	}
	if o := run(t, r, "search", map[string]string{"pattern": "original"}); strings.Contains(o.Result.Content, "link") {
		t.Fatalf("search must not follow symlinks: %s", o.Result.Content)
	}
	if b, _ := os.ReadFile(outside); string(b) != "original" {
		t.Fatal("host file was modified")
	}
}

func TestInRootSymlinkCannotBypassKernelBoundary(t *testing.T) {
	w := ws(t)
	r := NewRegistry(w.Policy, nil).Add(FileTools(w)...)
	os.WriteFile(filepath.Join(w.Root, "kernel", "agent.go"), []byte("package kernel"), 0o644)
	os.Symlink("../kernel", filepath.Join(w.Root, "organism", "link"))
	os.Symlink("../kernel/new.go", filepath.Join(w.Root, "organism", "dangling.go"))
	for _, p := range []string{"organism/link/agent.go", "organism/dangling.go"} {
		if o := run(t, r, "write_file", map[string]string{"path": p, "content": "pwned"}); !o.Result.IsError {
			t.Fatalf("write via %s must be refused: %+v", p, o.Result)
		}
		if o := run(t, r, "edit_file", map[string]string{"path": "organism/link/agent.go", "old_string": "package", "new_string": "x"}); !o.Result.IsError {
			t.Fatalf("edit via symlink must be refused")
		}
	}
	if b, _ := os.ReadFile(filepath.Join(w.Root, "kernel", "agent.go")); string(b) != "package kernel" {
		t.Fatal("kernel file modified through an in-root symlink")
	}
	if _, err := os.Stat(filepath.Join(w.Root, "kernel", "new.go")); err == nil {
		t.Fatal("kernel file created through a dangling symlink")
	}
	// Deleting the link itself is fine and leaves the target alone.
	if o := run(t, r, "delete_path", map[string]string{"path": "organism/link"}); o.Result.IsError {
		t.Fatal(o.Result.Content)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "kernel", "agent.go")); err != nil {
		t.Fatal("deleting a link must not delete its target")
	}
}
