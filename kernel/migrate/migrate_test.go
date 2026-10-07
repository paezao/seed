package migrate

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"seed/kernel/testutil"
)

func TestLoadValidation(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0001_a.sql": {Data: []byte("select 1")},
		"m/0001_b.sql": {Data: []byte("select 1")},
	}
	if _, err := Load(fsys, "m"); err == nil || !strings.Contains(err.Error(), "share a number") {
		t.Fatalf("expected duplicate number error, got %v", err)
	}
	bad := fstest.MapFS{"m/create things.sql": {Data: []byte("x")}}
	if _, err := Load(bad, "m"); err == nil {
		t.Fatal("expected bad name error")
	}
	if migs, err := Load(fstest.MapFS{}, "missing"); err != nil || len(migs) != 0 {
		t.Fatalf("missing dir should be empty: %v %v", migs, err)
	}
}

func TestApplyLifecycle(t *testing.T) {
	ctx := context.Background()
	url := testutil.Database(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	fsys := fstest.MapFS{
		"m/0001_tasks.sql": {Data: []byte("CREATE TABLE tasks (id serial primary key, title text not null);")},
	}
	migs, _ := Load(fsys, "m")
	res, err := Apply(ctx, conn, "schema_migrations", migs)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("first apply: %v %v", res, err)
	}
	res, err = Apply(ctx, conn, "schema_migrations", migs)
	if err != nil || len(res.Applied) != 0 || len(res.Already) != 1 {
		t.Fatalf("idempotent apply: %v %v", res, err)
	}

	// A failing migration rolls back and is not recorded.
	fsys["m/0002_bad.sql"] = &fstest.MapFile{Data: []byte("ALTER TABLE tasks ADD COLUMN priority text; ALTER TABLE nope ADD COLUMN x int;")}
	migs, _ = Load(fsys, "m")
	if _, err := Apply(ctx, conn, "schema_migrations", migs); err == nil || !strings.Contains(err.Error(), "0002_bad") {
		t.Fatalf("expected failure naming migration, got %v", err)
	}
	var n int
	_ = conn.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_name='tasks' AND column_name='priority'").Scan(&n)
	if n != 0 {
		t.Fatal("failed migration must be rolled back entirely")
	}

	// Editing an applied migration is refused.
	delete(fsys, "m/0002_bad.sql")
	fsys["m/0001_tasks.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE tasks (id bigserial primary key);")}
	migs, _ = Load(fsys, "m")
	if _, err := Apply(ctx, conn, "schema_migrations", migs); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("expected checksum error, got %v", err)
	}
}
