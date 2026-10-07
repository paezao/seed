// Package testutil holds helpers shared by kernel tests.
package testutil

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"seed/kernel/ids"
	"seed/kernel/infra"
	"seed/kernel/pg"
)

var (
	once     sync.Once
	adminURL string
	startErr error
)

// AdminURL is the PostgreSQL server used by tests: SEED_TEST_DATABASE_URL, or
// a private server started for this test binary (like a Seed's own), which
// stops when the test process exits. Empty when neither is possible.
func AdminURL() string {
	if v := os.Getenv("SEED_TEST_DATABASE_URL"); v != "" {
		return v
	}
	once.Do(func() {
		if !pg.Available() {
			return
		}
		pg.StopWithParent = true
		dir, err := os.MkdirTemp("", "seed-test-pg-")
		if err != nil {
			startErr = err
			return
		}
		s, err := pg.Start(context.Background(), dir)
		if err != nil {
			startErr = err
			return
		}
		adminURL = s.AdminURL()
	})
	return adminURL
}

// Database creates a fresh, uniquely named database and returns its URL.
// The test is skipped when no PostgreSQL is available.
func Database(t *testing.T) string {
	t.Helper()
	url := AdminURL()
	if url == "" {
		if startErr != nil {
			t.Fatalf("starting test PostgreSQL: %v", startErr)
		}
		t.Skip("no PostgreSQL: run the tests in the runtime image (make test) or set SEED_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin := infra.Admin{URL: url}
	if err := admin.Ping(ctx); err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	name := "seedtest_" + strings.ToLower(ids.Short(ids.New("x"), 10))
	if err := admin.EnsureDatabase(ctx, name, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.DropDatabase(context.Background(), name) })
	return admin.DatabaseURL(name, "", "", "")
}
