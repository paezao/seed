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

// The badge never sends anything itself: it only opens my control plane.
func TestBadgeOnlyNavigates(t *testing.T) {
	if n := strings.Count(badgeJS, "fetch("); n != 1 || !strings.Contains(badgeJS, "fetch('/_seed/badge'") {
		t.Errorf("badge.js should fetch only /_seed/badge (%d fetches)", n)
	}
	for _, bad := range []string{"method:", "XMLHttpRequest", "sendBeacon", "WebSocket", "EventSource"} {
		if strings.Contains(badgeJS, bad) {
			t.Errorf("badge.js contains %q", bad)
		}
	}
	if !strings.Contains(badgeJS, "'/_seed/#ask='") {
		t.Error("badge.js doesn't hand asks to the control plane")
	}
}
