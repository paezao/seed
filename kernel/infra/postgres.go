// Package infra manages the boring local infrastructure a Seed needs:
// a PostgreSQL server, a Docker network and the sandbox image.
package infra

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Admin performs administrative operations through a superuser connection URL.
type Admin struct {
	URL string
}

func (a Admin) conn(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, a.URL)
}

// Ping verifies the server is reachable.
func (a Admin) Ping(ctx context.Context) error {
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	return c.Ping(ctx)
}

// EnsureRole creates a login role (or resets its password).
func (a Admin) EnsureRole(ctx context.Context, role, password string) error {
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	var exists bool
	if err := c.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	// Passwords cannot be bound as parameters in DDL; quote as a literal.
	_, err = c.Exec(ctx, fmt.Sprintf(`%s ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %s`,
		verb, pgx.Identifier{role}.Sanitize(), quoteLiteral(password)))
	return err
}

// EnsureDatabase creates a database owned by owner (if non-empty) and, unless
// public is true, revokes default PUBLIC access so other Seeds' roles cannot connect.
func (a Admin) EnsureDatabase(ctx context.Context, name, owner string) error {
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	var exists bool
	if err := c.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		stmt := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
		if owner != "" {
			stmt += " OWNER " + pgx.Identifier{owner}.Sanitize()
		}
		if _, err := c.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := c.Exec(ctx, "REVOKE ALL ON DATABASE "+pgx.Identifier{name}.Sanitize()+" FROM PUBLIC"); err != nil {
		return err
	}
	if owner != "" {
		if _, err := c.Exec(ctx, "GRANT ALL ON DATABASE "+pgx.Identifier{name}.Sanitize()+" TO "+pgx.Identifier{owner}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}

// EnsureReader makes role a login role that can read the tables of db's
// public schema (owned by owner, including tables it creates later) and
// nothing else: no other database, no writes.
func (a Admin) EnsureReader(ctx context.Context, role, password, db, owner string) error {
	if err := a.EnsureRole(ctx, role, password); err != nil {
		return err
	}
	r := pgx.Identifier{role}.Sanitize()
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		"REVOKE pg_read_all_data FROM " + r, // granted by earlier versions
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{db}.Sanitize() + " TO " + r,
		"ALTER ROLE " + r + " SET default_transaction_read_only = on",
	} {
		if _, err := c.Exec(ctx, stmt); err != nil {
			c.Close(ctx)
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	c.Close(ctx)
	// Privileges inside the database itself.
	in, err := pgx.Connect(ctx, a.DatabaseURL(db, "", "", ""))
	if err != nil {
		return err
	}
	defer in.Close(ctx)
	o := pgx.Identifier{owner}.Sanitize()
	for _, stmt := range []string{
		"GRANT USAGE ON SCHEMA public TO " + r,
		"GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + r,
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + o + " IN SCHEMA public GRANT SELECT ON TABLES TO " + r,
	} {
		if _, err := in.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

// DropDatabase drops a database, terminating connections to it.
func (a Admin) DropDatabase(ctx context.Context, name string) error {
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	_, err = c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// ListDatabases returns database names with the given prefix.
func (a Admin) ListDatabases(ctx context.Context, prefix string) ([]string, error) {
	c, err := a.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close(ctx)
	rows, err := c.Query(ctx, `SELECT datname FROM pg_database WHERE starts_with(datname, $1) ORDER BY 1`, prefix)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// DatabaseURL derives a connection URL for database db from the admin URL,
// optionally replacing credentials and host. host may be "host:port" or an
// absolute Unix socket directory (e.g. the socket as mounted in a sandbox).
func (a Admin) DatabaseURL(db, user, password, host string) string {
	u, err := url.Parse(a.URL)
	if err != nil {
		return ""
	}
	u.Path = "/" + db
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	switch {
	case strings.HasPrefix(host, "/"):
		q := u.Query()
		q.Set("host", host)
		u.Host, u.RawQuery = "", q.Encode()
	case host != "":
		u.Host = host
	}
	return u.String()
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// RandomSecret returns n random bytes hex-encoded.
func RandomSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
