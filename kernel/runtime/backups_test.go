package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"seed/kernel/config"
	"seed/kernel/events"
	"seed/kernel/ids"
	"seed/kernel/infra"
	"seed/kernel/memory"
	"seed/kernel/testutil"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	store, err := memory.Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skip("pg_dump and pg_restore required")
	}
	admin := infra.Admin{URL: testutil.AdminURL()}
	cfg := &config.Config{Name: "bktest", Instance: strings.ToLower(ids.Short(ids.New("x"), 8)), Root: t.TempDir()}
	role, pass := cfg.DBName("organism"), "pw"
	if err := admin.EnsureRole(ctx, role, pass); err != nil {
		t.Fatal(err)
	}
	if err := admin.EnsureDatabase(ctx, cfg.DBName("app"), role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.DropDatabase(context.Background(), cfg.DBName("app")) })
	org := &Organism{Cfg: cfg, Admin: admin, Role: role, Pass: pass, Bus: events.NewBus()}
	grants := 0
	b := &Backups{Dir: filepath.Join(t.TempDir(), "backups"), Store: store, Bus: events.NewBus(), Admin: admin, Organism: org,
		Grants: func(context.Context) error { grants++; return nil }}

	sql := func(q string) string {
		t.Helper()
		c, err := pgx.Connect(ctx, org.DatabaseURL())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close(ctx)
		var out string
		if strings.HasPrefix(q, "SELECT") {
			if err := c.QueryRow(ctx, q).Scan(&out); err != nil {
				t.Fatal(err)
			}
			return out
		}
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
		return ""
	}
	sql("CREATE TABLE recipes (name text)")
	sql("INSERT INTO recipes VALUES ('pancakes')")
	bk, err := b.Take(ctx, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(b.Path(bk)); err != nil || st.Size() == 0 || bk.Size != st.Size() {
		t.Fatalf("backup file: %v %+v", err, bk)
	}
	if info, _ := os.Stat(b.Dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("backups are private: %v", info.Mode())
	}

	// Life goes on: more data, a new table.
	sql("INSERT INTO recipes VALUES ('waffles')")
	sql("CREATE TABLE later (x int)")

	safety, err := b.Restore(ctx, bk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := sql("SELECT string_agg(name, ',') FROM recipes"); got != "pancakes" {
		t.Fatalf("restored data: %q", got)
	}
	if got := sql("SELECT count(*)::text FROM pg_tables WHERE tablename = 'later'"); got != "0" {
		t.Fatal("a restore brings back exactly the backup: no newer tables")
	}
	if state, _ := org.State(); state != "restoring" || grants != 1 {
		t.Fatalf("app stopped for the restore (%s), permissions put back (%d)", state, grants)
	}
	// The restore can be undone: the data of just before was kept.
	if _, err := b.Restore(ctx, safety); err != nil {
		t.Fatal(err)
	}
	if got := sql("SELECT string_agg(name, ',' ORDER BY name) FROM recipes"); got != "pancakes,waffles" {
		t.Fatalf("undone restore: %q", got)
	}

	// Only so many of each kind are kept.
	for i := 0; i < keep["manual"]+2; i++ {
		if _, err := b.Take(ctx, "manual", ""); err != nil {
			t.Fatal(err)
		}
	}
	manual, _ := store.Backups(ctx, "manual")
	files, _ := filepath.Glob(filepath.Join(b.Dir, "*.dump"))
	all, _ := store.Backups(ctx, "")
	if len(manual) != keep["manual"] || len(files) != len(all) {
		t.Fatalf("kept %d manual copies, %d files for %d backups", len(manual), len(files), len(all))
	}
}
