package routines

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	KindAgent = "agent" // a prompt I run with my chat tools
	KindJob   = "job"   // a request to my organism's own endpoint

	// MinAgentGap keeps agent routines (which cost model tokens) sane.
	MinAgentGap = 15 * time.Minute
	MinJobGap   = time.Minute

	// File is where my organism declares its jobs.
	File = "organism/routines.yaml"
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	pathRe = regexp.MustCompile(`^/[A-Za-z0-9/_.~-]*$`)
)

// Job is one job my organism declares in organism/routines.yaml:
//
//	jobs:
//	  - name: send-reminders
//	    schedule: every 1h            # or cron: "0 8 * * *"
//	    path: /internal/jobs/send-reminders
//	    description: Email owners whose recipes have no photo.
type Job struct {
	Name        string `yaml:"name" json:"name"`
	Schedule    string `yaml:"schedule" json:"schedule"`
	Timezone    string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Method      string `yaml:"method,omitempty" json:"method,omitempty"`
	Path        string `yaml:"path" json:"path"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// ValidateName checks a routine name (kebab-case).
func ValidateName(n string) error {
	if !nameRe.MatchString(n) {
		return fmt.Errorf("%q: names are lowercase words joined by dashes (send-reminders)", n)
	}
	return nil
}

// ValidateSchedule parses a schedule and enforces the minimum gap for kind.
func ValidateSchedule(kind, spec, tz string) (Schedule, error) {
	s, err := Parse(spec, tz)
	if err != nil {
		return nil, err
	}
	min := MinJobGap
	if kind == KindAgent {
		min = MinAgentGap
	}
	if MinGap(s, time.Now()) < min {
		return nil, fmt.Errorf("%q runs too often: at most every %s", spec, strings.TrimSuffix(min.String(), "0s"))
	}
	return s, nil
}

// ValidateRequest checks a job's request: my organism's own endpoint.
func ValidateRequest(method, path string) (string, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "POST"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return "", fmt.Errorf("method %q is not supported", method)
	}
	if !pathRe.MatchString(path) || strings.Contains(path, "..") || strings.HasPrefix(path, "/_seed") {
		return "", fmt.Errorf("%q must be a path on my organism (like /internal/jobs/send-reminders)", path)
	}
	return method, nil
}

// LoadFile reads my organism's declared jobs from root (a checkout). A
// missing file means no jobs.
func LoadFile(root string) ([]Job, error) {
	b, err := os.ReadFile(filepath.Join(root, File))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 64<<10 {
		return nil, fmt.Errorf("%s is too large", File)
	}
	var doc struct {
		Jobs []Job `yaml:"jobs"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	if len(doc.Jobs) > 50 {
		return nil, fmt.Errorf("%s: at most 50 jobs", File)
	}
	seen := map[string]bool{}
	for i := range doc.Jobs {
		j := &doc.Jobs[i]
		if err := ValidateName(j.Name); err != nil {
			return nil, fmt.Errorf("%s: job %d: %w", File, i+1, err)
		}
		if seen[j.Name] {
			return nil, fmt.Errorf("%s: two jobs are named %s", File, j.Name)
		}
		seen[j.Name] = true
		if _, err := ValidateSchedule(KindJob, j.Schedule, j.Timezone); err != nil {
			return nil, fmt.Errorf("%s: job %s: %w", File, j.Name, err)
		}
		m, err := ValidateRequest(j.Method, j.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: job %s: %w", File, j.Name, err)
		}
		j.Method = m
	}
	return doc.Jobs, nil
}
