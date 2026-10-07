// Package skills loads the Seed's learned capabilities.
//
// A skill is a directory under skills/ containing SKILL.md (YAML front matter
// with name and description, then instructions) and optional supporting files
// (scripts/, templates/, tests/). Skills are ordinary files in the repository:
// the Seed creates and improves them during evolutions, and they are
// versioned with the generation that produced them.
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"seed/kernel/fsx"
)

type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Files       []string `json:"files"`
	Content     string   `json:"content,omitempty"`
}

type Library struct {
	Root string // repository root; skills live in Root/skills
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// List returns all skills, sorted by name. Malformed skills are reported
// with a description explaining the problem rather than hidden.
func (l Library) List() []Skill {
	dir := filepath.Join(l.Root, "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := l.load(e.Name(), false)
		if err != nil {
			out = append(out, Skill{Name: e.Name(), Description: "(invalid skill: " + err.Error() + ")", Path: "skills/" + e.Name()})
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one skill including its SKILL.md content.
func (l Library) Get(name string) (*Skill, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid skill name %q", name)
	}
	return l.load(name, true)
}

func (l Library) load(dirName string, withContent bool) (*Skill, error) {
	dir := filepath.Join(l.Root, "skills", dirName)
	b, err := fsx.ReadFile(l.Root, filepath.Join("skills", dirName, "SKILL.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("missing SKILL.md")
		}
		return nil, err
	}
	meta, body, err := ParseFrontMatter(string(b))
	if err != nil {
		return nil, err
	}
	s := &Skill{Name: meta["name"], Description: meta["description"], Path: "skills/" + dirName}
	if s.Name == "" {
		s.Name = dirName
	}
	if s.Description == "" {
		return nil, errors.New("SKILL.md front matter needs a description")
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(filepath.Join(l.Root), p)
			s.Files = append(s.Files, rel)
		}
		return nil
	})
	if withContent {
		s.Content = body
	}
	return s, nil
}

// ParseFrontMatter splits "---\nyaml\n---\nbody" documents.
func ParseFrontMatter(doc string) (map[string]string, string, error) {
	meta := map[string]string{}
	if !strings.HasPrefix(doc, "---") {
		return meta, doc, errors.New("SKILL.md must start with YAML front matter (---)")
	}
	rest := strings.TrimPrefix(doc, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, doc, errors.New("unterminated front matter")
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal([]byte(rest[:end]), &raw); err != nil {
		return meta, doc, fmt.Errorf("front matter: %w", err)
	}
	for k, v := range raw {
		meta[k] = strings.TrimSpace(fmt.Sprint(v))
	}
	body := strings.TrimLeft(rest[end+4:], "-\n")
	return meta, body, nil
}

// Index renders the skill list for an agent prompt.
func (l Library) Index() string {
	list := l.List()
	if len(list) == 0 {
		return "(no skills yet)"
	}
	var sb strings.Builder
	for _, s := range list {
		fmt.Fprintf(&sb, "- **%s** — %s\n", s.Name, s.Description)
	}
	return sb.String()
}
