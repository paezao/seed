// Package testutil holds helpers shared by kernel tests.
package testutil

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"seed/kernel/ids"
	"seed/kernel/infra"
)

// AdminURL is the PostgreSQL server used by tests (SEED_TEST_DATABASE_URL,
// defaulting to the local Seed infrastructure started by `make up`).
func AdminURL() string {
	if v := os.Getenv("SEED_TEST_DATABASE_URL"); v != "" {
		return v
	}
	u, err := infra.ManagedAdminURL()
	if err != nil {
		return ""
	}
	return u
}

// Database creates a fresh, uniquely named database and returns its URL.
// The test is skipped when PostgreSQL is unreachable.
func Database(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin := infra.Admin{URL: AdminURL()}
	if err := admin.Ping(ctx); err != nil {
		t.Skipf("PostgreSQL unavailable (%v); run `make up`", err)
	}
	name := "seedtest_" + strings.ToLower(ids.Short(ids.New("x"), 10))
	if err := admin.EnsureDatabase(ctx, name, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.DropDatabase(context.Background(), name) })
	return admin.DatabaseURL(name, "", "", "")
}
