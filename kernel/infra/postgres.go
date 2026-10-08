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
	if _, err = c.Exec(ctx, fmt.Sprintf(`%s ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %s`,
		verb, pgx.Identifier{role}.Sanitize(), quoteLiteral(password))); err != nil {
		return err
	}
	// A managed server's admin is not a superuser: it must be a member of
	// the roles it manages to create their databases and grant on their
	// tables (PostgreSQL 16+ gives it ADMIN OPTION on roles it created).
	var super bool
	if err := c.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		return err
	}
	if !super {
		var member bool
		if err := c.QueryRow(ctx, `SELECT pg_has_role(current_user, $1, 'USAGE')`, role).Scan(&member); err != nil {
			return err
		}
		if !member {
			if _, err := c.Exec(ctx, "GRANT "+pgx.Identifier{role}.Sanitize()+" TO CURRENT_USER"); err != nil {
				return fmt.Errorf("making the admin role a member of %s: %w", role, err)
			}
		}
	}
	return nil
}

// CheckPrivileges verifies the admin connection can do what a Seed needs:
// create databases (one per evolution) and roles (least privilege for the
// organism, evolutions and read-only queries).
func (a Admin) CheckPrivileges(ctx context.Context) error {
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	var user string
	var super, createDB, createRole bool
	if err := c.QueryRow(ctx, `SELECT current_user, rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user`).
		Scan(&user, &super, &createDB, &createRole); err != nil {
		return err
	}
	if super || (createDB && createRole) {
		return nil
	}
	var missing []string
	if !createDB {
		missing = append(missing, "CREATEDB")
	}
	if !createRole {
		missing = append(missing, "CREATEROLE")
	}
	return fmt.Errorf("the database role %q needs %s: I create a database per evolution and separate roles so my app can't read my memory "+
		"(as a superuser: ALTER ROLE %s %s)", user, strings.Join(missing, " and "), pgx.Identifier{user}.Sanitize(), strings.Join(missing, " "))
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
	// Earlier versions granted pg_read_all_data; take it back if so (only
	// then: revoking needs rights a managed server's admin may not have).
	var broad bool
	if err := c.QueryRow(ctx, `SELECT pg_has_role($1, 'pg_read_all_data', 'MEMBER')`, role).Scan(&broad); err != nil {
		c.Close(ctx)
		return err
	}
	if broad {
		if _, err := c.Exec(ctx, "REVOKE pg_read_all_data FROM "+r); err != nil {
			c.Close(ctx)
			return err
		}
	}
	for _, stmt := range []string{
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
//
// A URL for another role (user set) carries none of the admin's connection
// parameters except an allowlist: query parameters can hold credentials too
// (password=, user=, passfile=, service=, client keys) and take precedence
// over the URL's user part, so they would hand the admin's identity to code
// that gets this URL (my organism, evolutions).
func (a Admin) DatabaseURL(db, user, password, host string) string {
	u, err := url.Parse(a.URL)
	if err != nil {
		return ""
	}
	u.Path = "/" + db
	if user != "" {
		u.User = url.UserPassword(user, password)
		kept := url.Values{}
		for k, v := range u.Query() {
			if safeParams[k] {
				kept[k] = v
			}
		}
		u.RawQuery = kept.Encode()
	}
	switch {
	case strings.HasPrefix(host, "/"):
		// A Unix socket: TLS settings (and certificate paths, which are
		// mine, not the sandbox's) don't apply.
		q := u.Query()
		for _, k := range []string{"sslmode", "sslrootcert", "sslcert", "sslkey", "sslpassword", "port", "hostaddr"} {
			q.Del(k)
		}
		q.Set("host", host)
		q.Set("sslmode", "disable")
		u.Host, u.RawQuery = "", q.Encode()
	case host != "":
		u.Host = host
	}
	return u.String()
}

// safeParams may be copied from the admin URL into another role's URL.
var safeParams = map[string]bool{"sslmode": true, "connect_timeout": true, "application_name": true, "target_session_attrs": true}

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
