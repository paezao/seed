package infra_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"seed/kernel/infra"
	"seed/kernel/testutil"
)

func TestReaderCannotWrite(t *testing.T) {
	ctx := context.Background()
	dbURL := testutil.Database(t)
	admin := infra.Admin{URL: testutil.AdminURL()}
	dbName := strings.TrimPrefix(strings.SplitN(strings.SplitN(dbURL, "?", 2)[0], "@", 2)[1], "/")
	if i := strings.LastIndex(dbName, "/"); i >= 0 {
		dbName = dbName[i+1:]
	}
	c, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(ctx, "CREATE TABLE recipes (id int); INSERT INTO recipes VALUES (1)")
	c.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.EnsureReader(ctx, "seedtest_reader", "pw", dbName, "seed"); err != nil {
		t.Fatal(err)
	}
	r, err := pgx.Connect(ctx, admin.DatabaseURL(dbName, "seedtest_reader", "pw", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	var n int
	if err := r.QueryRow(ctx, "SELECT count(*) FROM recipes").Scan(&n); err != nil || n != 1 {
		t.Fatalf("reader should read: %d %v", n, err)
	}
	// Tables created later by the owner are readable too (default privileges).
	c2, _ := pgx.Connect(ctx, dbURL)
	_, _ = c2.Exec(ctx, "CREATE TABLE later (id int); INSERT INTO later VALUES (7)")
	c2.Close(ctx)
	if err := r.QueryRow(ctx, "SELECT id FROM later").Scan(&n); err != nil || n != 7 {
		t.Fatalf("reader should read tables created later: %v", err)
	}
	// It reads nothing outside this database's tables.
	other := testutil.Database(t)
	oc, _ := pgx.Connect(ctx, other)
	_, _ = oc.Exec(ctx, "CREATE TABLE secret (x text); INSERT INTO secret VALUES ('kernel memory')")
	oc.Close(ctx)
	otherName := other[strings.LastIndex(strings.SplitN(other, "?", 2)[0], "/")+1 : strings.Index(other, "?")]
	if rc, err := pgx.Connect(ctx, admin.DatabaseURL(otherName, "seedtest_reader", "pw", "")); err == nil {
		rc.Close(ctx)
		t.Fatal("the reader must not be able to connect to other databases")
	}
	// Even outside any read-only transaction, and even switching it off, writes are refused.
	for _, stmt := range []string{"DELETE FROM recipes", "SET default_transaction_read_only = off; DELETE FROM recipes", "CREATE TABLE evil (x int)"} {
		if _, err := r.Exec(ctx, stmt, pgx.QueryExecModeSimpleProtocol); err == nil {
			t.Errorf("reader must not be able to run %q", stmt)
		}
	}
}
