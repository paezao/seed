package pg

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivateServerLifecycle(t *testing.T) {
	if !Available() {
		t.Skip("PostgreSQL server binaries not installed (run tests in the runtime image: make test)")
	}
	ctx := context.Background()
	state := t.TempDir()
	s, err := Start(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, s.AdminURL())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(ctx, "CREATE TABLE memory (x int); INSERT INTO memory VALUES (42)")
	c.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(state, "secrets", "postgres-password")); info.Mode().Perm() != 0o600 {
		t.Fatal("password file must be 0600")
	}
	s.Stop()

	// Restart: same data, same password.
	s2, err := Start(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()
	c, err = pgx.Connect(ctx, s2.AdminURL())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var x int
	if err := c.QueryRow(ctx, "SELECT x FROM memory").Scan(&x); err != nil || x != 42 {
		t.Fatalf("data should survive restarts: %d %v", x, err)
	}
	wrong := (&Server{User: "seed", Password: "wrong", SocketDir: s2.SocketDir}).AdminURL()
	if _, err := pgx.Connect(ctx, wrong); err == nil {
		t.Fatal("wrong password must be refused")
	}
}
