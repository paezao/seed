package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The scripts I inject into my organism's pages must at least parse.
func TestInjectedScriptsParse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node isn't installed")
	}
	for name, src := range map[string]string{"badge.js": badgeJS, "preview.js": previewJS} {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(node, "--check", p).CombinedOutput(); err != nil {
			t.Errorf("%s doesn't parse: %v\n%s", name, err, out)
		}
	}
}

// Organism pages may not declare a charset, so my scripts are plain ASCII
// (anything else is written as an escape).
func TestInjectedScriptsAreASCII(t *testing.T) {
	for name, src := range map[string]string{"badge.js": badgeJS, "preview.js": previewJS} {
		for i, r := range src {
			if r > 0x7f {
				t.Errorf("%s: %q at byte %d; write it as an escape", name, r, i)
				break
			}
		}
	}
}

// The badge never sends a message itself: it asks whether to show itself,
// leaves screenshots as drafts, and opens my control plane.
func TestBadgeOnlyNavigates(t *testing.T) {
	if n := strings.Count(badgeJS, "fetch("); n != 2 || !strings.Contains(badgeJS, "fetch('/_seed/badge'") || !strings.Contains(badgeJS, "fetch('/_seed/ask-draft'") {
		t.Errorf("badge.js should fetch only /_seed/badge and /_seed/ask-draft (%d fetches)", n)
	}
	if strings.Count(badgeJS, "method:") != 1 {
		t.Error("badge.js should send nothing but screenshot drafts")
	}
	for _, bad := range []string{"XMLHttpRequest", "sendBeacon", "WebSocket", "EventSource", "/_seed/api/messages", "/_seed/api/evolutions"} {
		if strings.Contains(badgeJS, bad) {
			t.Errorf("badge.js contains %q", bad)
		}
	}
	if !strings.Contains(badgeJS, "'/_seed/#ask='") {
		t.Error("badge.js doesn't hand asks to the control plane")
	}
}
