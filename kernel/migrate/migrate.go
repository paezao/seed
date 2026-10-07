// Package migrate applies ordered SQL migrations to a PostgreSQL database.
//
// Migrations are files named NNNN_description.sql. Each runs in its own
// transaction and is recorded with a checksum; editing an applied migration
// is an error, because history must not be rewritten under a live database.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var fileRe = regexp.MustCompile(`^(\d+)_[A-Za-z0-9_\-]+\.sql$`)

type Migration struct {
	Version  string // file name without .sql
	SQL      string
	Checksum string
}

// Load reads migrations from fsys (in a directory) in lexical order.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if !fileRe.MatchString(e.Name()) {
			return nil, fmt.Errorf("migration %q: name must look like 0001_create_things.sql", e.Name())
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, Migration{Version: strings.TrimSuffix(e.Name(), ".sql"), SQL: string(b), Checksum: hex.EncodeToString(sum[:8])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if prefix(out[i].Version) == prefix(out[i-1].Version) {
			return nil, fmt.Errorf("migrations %s and %s share a number", out[i-1].Version, out[i].Version)
		}
	}
	return out, nil
}

func prefix(v string) string { return strings.SplitN(v, "_", 2)[0] }

type Result struct {
	Applied []string // versions applied by this run
	Already []string // versions that were already applied
}

func (r Result) String() string {
	if len(r.Applied) == 0 {
		return fmt.Sprintf("no new migrations (%d already applied)", len(r.Already))
	}
	return fmt.Sprintf("applied %s (%d already applied)", strings.Join(r.Applied, ", "), len(r.Already))
}

// Apply runs pending migrations. table names the bookkeeping table.
func Apply(ctx context.Context, conn *pgx.Conn, table string, migs []Migration) (Result, error) {
	var res Result
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`, pgx.Identifier{table}.Sanitize())); err != nil {
		return res, err
	}
	applied := map[string]string{}
	rows, err := conn.Query(ctx, fmt.Sprintf(`SELECT version, checksum FROM %s`, pgx.Identifier{table}.Sanitize()))
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var v, c string
		if err := rows.Scan(&v, &c); err != nil {
			rows.Close()
			return res, err
		}
		applied[v] = c
	}
	rows.Close()
	for _, m := range migs {
		if sum, ok := applied[m.Version]; ok {
			if sum != m.Checksum {
				return res, fmt.Errorf("migration %s was modified after being applied; create a new migration instead", m.Version)
			}
			res.Already = append(res.Already, m.Version)
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return res, err
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return res, fmt.Errorf("migration %s failed: %w", m.Version, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (version, checksum) VALUES ($1, $2)`, pgx.Identifier{table}.Sanitize()), m.Version, m.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return res, err
		}
		if err := tx.Commit(ctx); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, m.Version)
	}
	return res, nil
}

// ApplyURL connects to url and applies migrations from a directory on disk.
func ApplyURL(ctx context.Context, url, table, dir string) (Result, error) {
	migs, err := Load(os.DirFS(dir), ".")
	if err != nil {
		return Result{}, err
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close(ctx)
	return Apply(ctx, conn, table, migs)
}
