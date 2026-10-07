package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeDoesNotFollowEscapingSymlinks(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "knowledge"), 0o755)
	os.WriteFile(filepath.Join(root, SelfPath), []byte("identity:\n  name: Tasklet\n"), 0o644)
	secret := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600)
	os.Symlink(secret, filepath.Join(root, "knowledge", "notes.md"))
	if b := Brief(root); strings.Contains(b, "PRIVATE KEY") {
		t.Fatal("knowledge brief leaked a host file through a symlink")
	}
	if _, err := ReadFile(root, "knowledge/notes.md"); err == nil {
		t.Fatal("ReadFile must refuse escaping symlinks")
	}
	if Name(root) != "Tasklet" {
		t.Fatal("name should still be read")
	}
}
