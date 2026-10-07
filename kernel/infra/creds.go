package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

// ConfigDir is the owner's Seed configuration directory (~/.config/seed),
// shared by all their Seeds. It holds secrets, so it is created 0700.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "seed")
	return dir, os.MkdirAll(dir, 0o700)
}

// ReadSecretJSON / WriteSecretJSON store small JSON documents with mode 0600.
func ReadSecretJSON(name string, v any) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func WriteSecretJSON(name string, v any) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// legacyPassword is the superuser password of Seed installations before
// credentials were managed; EnsurePostgres rotates it away.
const legacyPassword = "seed"

// ManagedAdminURL returns the superuser URL of the shared PostgreSQL. Its
// password is random, generated once and kept in ~/.config/seed/postgres.json:
// sandboxed code can reach the server over the sandbox network, so the
// password must never be guessable or present in any Seed's repository.
func ManagedAdminURL() (string, error) {
	var c struct {
		Password string `json:"password"`
	}
	if err := ReadSecretJSON("postgres.json", &c); err != nil || c.Password == "" {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		c.Password = RandomSecret(24)
		if err := WriteSecretJSON("postgres.json", c); err != nil {
			return "", err
		}
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword("seed", c.Password),
		Host: "127.0.0.1:" + PostgresHostPort, Path: "/postgres", RawQuery: "sslmode=disable"}
	return u.String(), nil
}

// rotateLegacy changes a legacy superuser password to the managed one.
func rotateLegacy(ctx context.Context, admin Admin) error {
	u, err := url.Parse(admin.URL)
	if err != nil {
		return err
	}
	pw, _ := u.User.Password()
	legacy := *u
	legacy.User = url.UserPassword(u.User.Username(), legacyPassword)
	c, err := pgx.Connect(ctx, legacy.String())
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	_, err = c.Exec(ctx, fmt.Sprintf("ALTER ROLE %s PASSWORD %s", pgx.Identifier{u.User.Username()}.Sanitize(), quoteLiteral(pw)))
	return err
}

func passwordOf(adminURL string) string {
	u, err := url.Parse(adminURL)
	if err != nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}
