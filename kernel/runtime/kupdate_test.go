package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"seed/kernel/events"
	"seed/kernel/git"
	"seed/kernel/memory"
	"seed/kernel/template"
	"seed/kernel/testutil"
	"seed/kernel/update"
)

func TestKernelUpdates(t *testing.T) {
	if !template.Available() {
		t.Skip("binary built without a template")
	}
	ctx := context.Background()
	store, err := memory.Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := template.Create(ctx, root, "tasks"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, template.VersionPath), []byte("old\n"), 0o644)
	if _, err := gitCommit(ctx, root, "upgrade: pretend an older kernel\n\nKernel: old"); err != nil {
		t.Fatal(err)
	}
	archive, _ := template.Archive()
	dockerfile, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	newVersion, _ := template.Version()
	m := update.Manifest{Version: newVersion, PublishedAt: time.Now().UTC().Truncate(time.Second), Notes: "Faster.",
		Template: update.RuntimeHash(archive), TemplateSize: int64(len(archive)), Runtime: update.RuntimeHash(dockerfile)}
	manifest, _ := json.Marshal(m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(manifest)
		case "/manifest.json.sig":
			w.Write(update.Sign(manifest, priv))
		case "/template.tar.gz":
			w.Write(archive)
		}
	}))
	defer srv.Close()
	restarted := false
	u := &KernelUpdates{Root: root, Store: store, Bus: events.NewBus(), Restart: func() { restarted = true },
		Client: &update.Client{Source: srv.URL, Keys: []ed25519.PublicKey{pub}}}

	s, err := u.Check(ctx)
	if err != nil || !s.Available || s.NeedsRuntime || s.Latest.Notes != "Faster." {
		t.Fatalf("a newer release is available: %+v %v", s, err)
	}
	u.Busy = func() bool { return true }
	if _, err := u.Apply(ctx, false); err == nil {
		t.Fatal("no update while evolving")
	}
	u.Busy = nil
	res, err := u.Apply(ctx, false)
	if err != nil || res.To != newVersion || !restarted {
		t.Fatalf("apply: %+v %v restarted=%v", res, err, restarted)
	}
	// After the restart: the new kernel reports success and isn't offered again.
	u2 := &KernelUpdates{Root: root, Store: store, Bus: events.NewBus(), Client: u.Client}
	u2.reportBoot(ctx)
	if u2.lastEvent == nil || !u2.lastEvent.OK {
		t.Fatalf("success is reported: %+v", u2.lastEvent)
	}
	if s, _ := u2.Check(ctx); s.Available {
		t.Fatal("the installed release is not offered again")
	}

	// A release with a different runtime needs a redeploy, not an in-place update.
	m.Runtime = update.RuntimeHash([]byte("FROM somethingelse"))
	m.Version, m.PublishedAt = "next", m.PublishedAt.Add(time.Hour)
	manifest, _ = json.Marshal(m)
	if s, _ := u2.Check(ctx); !s.Available || !s.NeedsRuntime {
		t.Fatalf("runtime change: %+v", s)
	}
	if _, err := u2.Apply(ctx, false); err == nil {
		t.Fatal("must not install a kernel that needs a new runtime image")
	}

	// A rollback by the boot script is reported, and that version isn't offered again.
	_ = store.SetSetting(ctx, settingPending, "next")
	os.MkdirAll(filepath.Join(root, ".seed"), 0o755)
	os.WriteFile(filepath.Join(root, ".seed", "kernel-rollback.json"), []byte(`{"from":"next","to":"`+newVersion+`","reason":"didn't build","generation":4}`), 0o600)
	u3 := &KernelUpdates{Root: root, Store: store, Bus: events.NewBus(), Client: u.Client}
	u3.reportBoot(ctx)
	if u3.lastEvent == nil || u3.lastEvent.OK {
		t.Fatalf("the rollback is reported: %+v", u3.lastEvent)
	}
	m.Runtime = update.RuntimeHash(dockerfile)
	manifest, _ = json.Marshal(m)
	if s, _ := u3.Check(ctx); s.Available {
		t.Fatal("a version that failed to start is not offered again")
	}
}

func gitCommit(ctx context.Context, root, msg string) (string, error) {
	return git.Open(root).CommitAll(ctx, msg)
}
