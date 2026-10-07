// Package knowledge reads the Seed's durable understanding of itself.
//
// knowledge/self.yaml is the machine-readable self model ("what am I?").
// knowledge/*.md hold identity, product and architecture narratives, and
// knowledge/decisions/ holds architecture decision records. These files are
// written by the Seed during reflection and committed with each generation,
// so the Seed never has to reconstruct its purpose from source code.
package knowledge

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Self is the self model. Unknown fields are preserved by working on the raw
// YAML; this struct is only for reading the parts the kernel needs.
type Self struct {
	Identity struct {
		// Slug is the stable technical name (matches seed.yaml); Name is the
		// display name the Seed currently goes by and evolves with it.
		Slug    string `yaml:"slug" json:"slug"`
		Name    string `yaml:"name" json:"name"`
		Tagline string `yaml:"tagline" json:"tagline"`
		Accent  string `yaml:"accent" json:"accent"`
	} `yaml:"identity" json:"identity"`
	Purpose struct {
		Description string `yaml:"description" json:"description"`
	} `yaml:"purpose" json:"purpose"`
	Capabilities []any          `yaml:"capabilities" json:"capabilities"`
	Architecture map[string]any `yaml:"architecture" json:"architecture"`
	Constraints  []any          `yaml:"constraints" json:"constraints"`
	Goals        []any          `yaml:"goals" json:"goals"`
	Extra        map[string]any `yaml:",inline" json:"-"`
}

type Doc struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type Knowledge struct {
	SelfRaw string `json:"self"`
	Self    *Self  `json:"-"`
	// SelfModel is the parsed self model as generic data, for display.
	SelfModel map[string]any `json:"self_model"`
	Docs      []Doc          `json:"docs"`
	Decisions []Doc          `json:"decisions"`
}

const (
	SelfPath = "knowledge/self.yaml"
	LogoPath = "knowledge/logo.svg"
)

// Load reads knowledge from a repository root.
func Load(root string) (*Knowledge, error) {
	k := &Knowledge{}
	b, err := os.ReadFile(filepath.Join(root, SelfPath))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	k.SelfRaw = string(b)
	if s, err := ParseSelf(b); err == nil {
		k.Self = s
	}
	_ = yaml.Unmarshal(b, &k.SelfModel)
	k.Docs = listDocs(root, "knowledge")
	k.Decisions = listDocs(root, "knowledge/decisions")
	return k, nil
}

// ParseSelf parses and validates the self model.
func ParseSelf(b []byte) (*Self, error) {
	var s Self
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("self.yaml: %w", err)
	}
	if s.Identity.Name == "" {
		return nil, errors.New("self.yaml: identity.name is required")
	}
	return &s, nil
}

// ValidateSelf checks the self model in root.
func ValidateSelf(root string) error {
	b, err := os.ReadFile(filepath.Join(root, SelfPath))
	if err != nil {
		return err
	}
	_, err = ParseSelf(b)
	return err
}

func listDocs(root, dir string) []Doc {
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil
	}
	var docs []Doc
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
		docs = append(docs, Doc{Path: rel, Title: title(filepath.Join(root, rel), e.Name())})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs
}

func title(path, fallback string) string {
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return strings.TrimSuffix(fallback, ".md")
}

// ReadFile reads a knowledge file by repository-relative path, refusing
// anything outside knowledge/.
func ReadFile(root, rel string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if !strings.HasPrefix(clean, "knowledge/") || strings.Contains(clean, "..") {
		return "", fmt.Errorf("not a knowledge path: %s", rel)
	}
	b, err := os.ReadFile(filepath.Join(root, clean))
	return string(b), err
}

// Purpose returns the purpose description, or "" if the Seed has none yet.
func (k *Knowledge) Purpose() string {
	if k == nil || k.Self == nil {
		return ""
	}
	return strings.TrimSpace(k.Self.Purpose.Description)
}

// Brief renders knowledge for inclusion in an agent prompt: the self model and
// the narrative documents in full (they are meant to stay short), plus the
// list of decisions.
func Brief(root string) string {
	k, err := Load(root)
	if err != nil {
		return "(knowledge unavailable: " + err.Error() + ")"
	}
	var sb strings.Builder
	sb.WriteString("### knowledge/self.yaml\n```yaml\n" + strings.TrimSpace(k.SelfRaw) + "\n```\n\n")
	for _, d := range k.Docs {
		b, err := os.ReadFile(filepath.Join(root, d.Path))
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(b))
		if len(content) > 12000 {
			content = content[:12000] + "\n…(truncated; read the file for the rest)"
		}
		fmt.Fprintf(&sb, "### %s\n%s\n\n", d.Path, content)
	}
	sb.WriteString("### Decisions (knowledge/decisions/)\n")
	if len(k.Decisions) == 0 {
		sb.WriteString("(none yet)\n")
	}
	for _, d := range k.Decisions {
		fmt.Fprintf(&sb, "- %s — %s\n", d.Path, d.Title)
	}
	return sb.String()
}

// Name returns the name the Seed currently goes by ("Seed" until it becomes something).
func Name(root string) string {
	b, err := os.ReadFile(filepath.Join(root, SelfPath))
	if err != nil {
		return "Seed"
	}
	s, err := ParseSelf(b)
	if err != nil || strings.TrimSpace(s.Identity.Name) == "" {
		return "Seed"
	}
	return strings.TrimSpace(s.Identity.Name)
}
