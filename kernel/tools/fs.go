package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"seed/kernel/permissions"
)

// Workspace confines file operations to a root directory.
type Workspace struct {
	Root   string
	Policy *permissions.Policy
	// WriteFilter optionally restricts writes further (e.g. reflection may only
	// touch knowledge/ and skills/). It returns an error to refuse.
	WriteFilter func(rel string) error
}

// skipDirs are never listed or searched: dependencies and build outputs.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "dist": true, ".seed": true, "bin": true, ".vite": true}

// Resolve maps a user path (relative, or absolute under Root or /workspace)
// to an absolute host path inside Root, rejecting escapes and .git.
func (w *Workspace) Resolve(p string) (abs, rel string, err error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." || p == "/workspace" {
		return w.Root, ".", nil
	}
	switch {
	case strings.HasPrefix(p, "/workspace/"):
		p = strings.TrimPrefix(p, "/workspace/")
	case filepath.IsAbs(p):
		r, err := filepath.Rel(w.Root, p)
		if err != nil || strings.HasPrefix(r, "..") {
			return "", "", fmt.Errorf("path %q is outside the workspace", p)
		}
		p = r
	}
	rel = filepath.Clean(p)
	if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("path %q escapes the workspace", p)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".git" {
			return "", "", errors.New("the .git directory is managed by the kernel; use the git tools")
		}
	}
	abs = filepath.Join(w.Root, rel)
	// Resolve symlinks of the deepest existing ancestor to prevent escapes.
	rootReal, err := filepath.EvalSymlinks(w.Root)
	if err != nil {
		return "", "", err
	}
	probe := abs
	for {
		if real, err := filepath.EvalSymlinks(probe); err == nil {
			if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
				return "", "", fmt.Errorf("path %q resolves outside the workspace", p)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return abs, rel, nil
}

// writeLevel classifies a write to rel.
func (w *Workspace) writeLevel(rel, verb string) (permissions.Level, string) {
	action := verb + " " + rel
	if w.Policy != nil && w.Policy.IsProtected(rel) {
		return permissions.Dangerous, action + " (kernel)"
	}
	return permissions.Review, action
}

func (w *Workspace) checkWrite(rel string) error {
	if w.WriteFilter != nil {
		return w.WriteFilter(rel)
	}
	return nil
}

func pathClassifier(w *Workspace, verb string) func(json.RawMessage) (permissions.Level, string) {
	return func(in json.RawMessage) (permissions.Level, string) {
		var a struct{ Path string }
		_ = json.Unmarshal(in, &a)
		_, rel, err := w.Resolve(a.Path)
		if err != nil {
			return permissions.Review, verb + " " + a.Path
		}
		return w.writeLevel(rel, verb)
	}
}

// FileTools returns read/write/edit/list/search/delete tools over the workspace.
func FileTools(w *Workspace) []*Tool {
	return append(ReadOnlyFileTools(w), WriteFileTools(w)...)
}

// ReadOnlyFileTools returns the non-mutating file tools.
func ReadOnlyFileTools(w *Workspace) []*Tool {
	return []*Tool{
		{
			Name:        "read_file",
			Description: "Read a text file from the workspace. Returns numbered lines (the numbers are not part of the file). Use offset/limit for large files.",
			Schema: Schema(Props{
				"path":   Str("path relative to the workspace root"),
				"offset": Int("1-based line to start at (default 1)"),
				"limit":  Int("maximum lines to return (default 2000)"),
			}, "path"),
			Classify: Fixed(permissions.Safe, "read file"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct {
					Path          string
					Offset, Limit int
				}
				if err := decode(in, &a); err != nil {
					return "", err
				}
				abs, rel, err := w.Resolve(a.Path)
				if err != nil {
					return "", err
				}
				b, err := os.ReadFile(abs)
				if err != nil {
					return "", err
				}
				if strings.IndexByte(string(b[:min(len(b), 8000)]), 0) >= 0 {
					return "", fmt.Errorf("%s looks like a binary file (%d bytes)", rel, len(b))
				}
				lines := strings.Split(string(b), "\n")
				if a.Offset < 1 {
					a.Offset = 1
				}
				if a.Limit <= 0 {
					a.Limit = 2000
				}
				var sb strings.Builder
				end := min(len(lines), a.Offset-1+a.Limit)
				for i := a.Offset - 1; i < end; i++ {
					line := lines[i]
					if len(line) > 2000 {
						line = line[:2000] + "…"
					}
					fmt.Fprintf(&sb, "%6d\t%s\n", i+1, line)
				}
				if end < len(lines) {
					fmt.Fprintf(&sb, "… (%d more lines; use offset=%d)\n", len(lines)-end, end+1)
				}
				if len(b) == 0 {
					return "(empty file)", nil
				}
				return sb.String(), nil
			},
		},
		{
			Name:        "list_dir",
			Description: "List a directory tree (skips .git, node_modules, dist, bin).",
			Schema: Schema(Props{
				"path":  Str("directory relative to the workspace root (default: root)"),
				"depth": Int("how many levels to descend (default 2, max 6)"),
			}),
			Classify: Fixed(permissions.Safe, "list directory"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct {
					Path  string
					Depth int
				}
				if err := decode(in, &a); err != nil {
					return "", err
				}
				abs, rel, err := w.Resolve(a.Path)
				if err != nil {
					return "", err
				}
				if a.Depth <= 0 {
					a.Depth = 2
				}
				a.Depth = min(a.Depth, 6)
				var sb strings.Builder
				sb.WriteString(rel + "/\n")
				count := 0
				var walk func(dir string, depth int, indent string)
				walk = func(dir string, depth int, indent string) {
					entries, err := os.ReadDir(dir)
					if err != nil {
						return
					}
					for _, e := range entries {
						if count > 500 {
							return
						}
						count++
						if e.IsDir() {
							if skipDirs[e.Name()] {
								fmt.Fprintf(&sb, "%s%s/ (skipped)\n", indent, e.Name())
								continue
							}
							fmt.Fprintf(&sb, "%s%s/\n", indent, e.Name())
							if depth > 1 {
								walk(filepath.Join(dir, e.Name()), depth-1, indent+"  ")
							}
						} else {
							info, _ := e.Info()
							size := int64(0)
							if info != nil {
								size = info.Size()
							}
							fmt.Fprintf(&sb, "%s%s (%d B)\n", indent, e.Name(), size)
						}
					}
				}
				walk(abs, a.Depth, "  ")
				if count > 500 {
					sb.WriteString("… (truncated)\n")
				}
				return sb.String(), nil
			},
		},
		{
			Name:        "search",
			Description: "Search file contents with a regular expression (RE2 syntax). Returns path:line: text matches.",
			Schema: Schema(Props{
				"pattern": Str("regular expression"),
				"path":    Str("directory or file to search (default: root)"),
				"glob":    Str("only files whose name matches this glob, e.g. *.go"),
			}, "pattern"),
			Classify: Fixed(permissions.Safe, "search files"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Pattern, Path, Glob string }
				if err := decode(in, &a); err != nil {
					return "", err
				}
				re, err := regexp.Compile(a.Pattern)
				if err != nil {
					return "", err
				}
				abs, _, err := w.Resolve(a.Path)
				if err != nil {
					return "", err
				}
				var matches []string
				_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
					if err != nil || len(matches) >= 200 {
						return nil
					}
					if d.IsDir() {
						if skipDirs[d.Name()] {
							return filepath.SkipDir
						}
						return nil
					}
					if a.Glob != "" {
						if ok, _ := filepath.Match(a.Glob, d.Name()); !ok {
							return nil
						}
					}
					info, err := d.Info()
					if err != nil || info.Size() > 1<<20 {
						return nil
					}
					b, err := os.ReadFile(p)
					if err != nil {
						return nil
					}
					rel, _ := filepath.Rel(w.Root, p)
					for i, line := range strings.Split(string(b), "\n") {
						if re.MatchString(line) {
							if len(line) > 300 {
								line = line[:300] + "…"
							}
							matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, i+1, line))
							if len(matches) >= 200 {
								break
							}
						}
					}
					return nil
				})
				if len(matches) == 0 {
					return "no matches", nil
				}
				return strings.Join(matches, "\n"), nil
			},
		},
	}
}

// WriteFileTools returns the mutating file tools.
func WriteFileTools(w *Workspace) []*Tool {
	return []*Tool{
		{
			Name:        "write_file",
			Description: "Create or overwrite a file with the given content (parent directories are created). Prefer edit_file for small changes to existing files.",
			Schema: Schema(Props{
				"path":    Str("path relative to the workspace root"),
				"content": Str("complete file content"),
			}, "path", "content"),
			Classify: pathClassifier(w, "write"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Path, Content string }
				if err := decode(in, &a); err != nil {
					return "", err
				}
				abs, rel, err := w.Resolve(a.Path)
				if err != nil {
					return "", err
				}
				if err := w.checkWrite(rel); err != nil {
					return "", err
				}
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return "", err
				}
				_, existed := os.Stat(abs)
				if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
					return "", err
				}
				verb := "created"
				if existed == nil {
					verb = "overwrote"
				}
				return fmt.Sprintf("%s %s (%d lines)", verb, rel, strings.Count(a.Content, "\n")+1), nil
			},
		},
		{
			Name:        "edit_file",
			Description: "Replace an exact string in a file. old_string must match exactly once (including whitespace) unless replace_all is true. Include enough surrounding context to make it unique.",
			Schema: Schema(Props{
				"path":        Str("path relative to the workspace root"),
				"old_string":  Str("exact text to replace"),
				"new_string":  Str("replacement text"),
				"replace_all": Bool("replace every occurrence"),
			}, "path", "old_string", "new_string"),
			Classify: pathClassifier(w, "edit"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct {
					Path       string
					OldString  string `json:"old_string"`
					NewString  string `json:"new_string"`
					ReplaceAll bool   `json:"replace_all"`
				}
				if err := decode(in, &a); err != nil {
					return "", err
				}
				abs, rel, err := w.Resolve(a.Path)
				if err != nil {
					return "", err
				}
				if err := w.checkWrite(rel); err != nil {
					return "", err
				}
				b, err := os.ReadFile(abs)
				if err != nil {
					return "", err
				}
				s := string(b)
				if a.OldString == "" {
					return "", errors.New("old_string must not be empty (use write_file to create files)")
				}
				n := strings.Count(s, a.OldString)
				switch {
				case n == 0:
					return "", fmt.Errorf("old_string not found in %s; re-read the file and copy the text exactly", rel)
				case n > 1 && !a.ReplaceAll:
					return "", fmt.Errorf("old_string matches %d times in %s; add context or set replace_all", n, rel)
				}
				if a.ReplaceAll {
					s = strings.ReplaceAll(s, a.OldString, a.NewString)
				} else {
					s = strings.Replace(s, a.OldString, a.NewString, 1)
				}
				if err := os.WriteFile(abs, []byte(s), 0o644); err != nil {
					return "", err
				}
				return fmt.Sprintf("edited %s (%d replacement(s))", rel, max(1, n*boolInt(a.ReplaceAll))), nil
			},
		},
		{
			Name:        "delete_path",
			Description: "Delete a file or directory (recursively).",
			Schema:      Schema(Props{"path": Str("path relative to the workspace root")}, "path"),
			Classify:    pathClassifier(w, "delete"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Path string }
				if err := decode(in, &a); err != nil {
					return "", err
				}
				abs, rel, err := w.Resolve(a.Path)
				if err != nil {
					return "", err
				}
				if rel == "." {
					return "", errors.New("refusing to delete the workspace root")
				}
				if err := w.checkWrite(rel); err != nil {
					return "", err
				}
				if _, err := os.Lstat(abs); err != nil {
					return "", err
				}
				return "deleted " + rel, os.RemoveAll(abs)
			},
		},
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Tree renders a compact listing of files under dir (relative to root),
// used to give agents an overview without a tool call.
func Tree(root, dir string, maxEntries int) string {
	var lines []string
	base := filepath.Join(root, dir)
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			lines = append(lines, rel)
		}
		return nil
	})
	sort.Strings(lines)
	if len(lines) > maxEntries {
		extra := len(lines) - maxEntries
		lines = append(lines[:maxEntries], fmt.Sprintf("… and %d more files", extra))
	}
	return strings.Join(lines, "\n")
}
