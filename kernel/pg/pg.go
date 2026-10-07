// Package pg runs a Seed's own private PostgreSQL.
//
// Every Seed keeps its data inside its own folder (.seed/postgres): kernel
// memory, the live organism's database, and scratch databases for
// evolutions. The server listens only on a Unix socket in .seed/run (no TCP
// port) and requires a password, kept in .seed/secrets, which sandboxed code
// never sees. Nothing is shared with other Seeds.
package pg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

// StopWithParent makes servers stop when the starting process dies (for tests).
var StopWithParent bool

// Server is a running private PostgreSQL.
type Server struct {
	DataDir   string
	SocketDir string
	User      string
	Password  string
	cmd       *exec.Cmd
	done      chan error
}

// Available reports whether PostgreSQL server binaries are on PATH.
func Available() bool {
	_, err := exec.LookPath("initdb")
	if err != nil {
		return false
	}
	_, err = exec.LookPath("postgres")
	return err == nil
}

// Start initializes (on first use) and starts the private server for the
// Seed whose state directory is stateDir (normally <seed>/.seed).
func Start(ctx context.Context, stateDir string) (*Server, error) {
	if !Available() {
		return nil, errors.New("PostgreSQL server binaries (initdb, postgres) are not installed; run me in my container (seed run)")
	}
	s := &Server{
		DataDir:   filepath.Join(stateDir, "postgres"),
		SocketDir: filepath.Join(stateDir, "run"),
		User:      "seed",
	}
	for _, d := range []string{filepath.Join(stateDir, "secrets"), s.SocketDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	pw, err := password(filepath.Join(stateDir, "secrets", "postgres-password"))
	if err != nil {
		return nil, err
	}
	s.Password = pw
	if _, err := os.Stat(filepath.Join(s.DataDir, "PG_VERSION")); os.IsNotExist(err) {
		if err := s.initdb(ctx); err != nil {
			return nil, err
		}
	}
	if err := s.start(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func password(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	pw := randomHex(24)
	return pw, os.WriteFile(path, []byte(pw), 0o600)
}

func (s *Server) initdb(ctx context.Context) error {
	slog.Info("creating my private database", "dir", s.DataDir)
	pwfile, err := os.CreateTemp("", "seed-pw-")
	if err != nil {
		return err
	}
	defer os.Remove(pwfile.Name())
	if _, err := pwfile.WriteString(s.Password); err != nil {
		return err
	}
	pwfile.Close()
	cmd := exec.CommandContext(ctx, "initdb", "-D", s.DataDir, "-U", s.User, "-A", "scram-sha-256",
		"--pwfile="+pwfile.Name(), "-E", "UTF8", "--no-locale", "--no-instructions")
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(s.DataDir)
		return fmt.Errorf("initdb: %w: %s", err, out)
	}
	return nil
}

func (s *Server) start(ctx context.Context) error {
	logf, err := os.OpenFile(filepath.Join(filepath.Dir(s.DataDir), "postgres.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command("postgres", "-D", s.DataDir, "-k", s.SocketDir,
		"-c", "listen_addresses=", "-c", "unix_socket_permissions=0700",
		"-c", "max_connections=100", "-c", "shared_buffers=64MB")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = procAttr(StopWithParent)
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	s.cmd = cmd
	s.done = make(chan error, 1)
	go func() { s.done <- cmd.Wait(); logf.Close() }()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-s.done:
			b, _ := os.ReadFile(filepath.Join(filepath.Dir(s.DataDir), "postgres.log"))
			return fmt.Errorf("postgres exited during startup (%v): %s", err, tail(string(b), 1500))
		default:
		}
		c, err := pgx.Connect(ctx, s.AdminURL())
		if err == nil {
			c.Close(ctx)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("postgres did not become ready")
}

// AdminURL is the superuser connection URL (over the socket).
func (s *Server) AdminURL() string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(s.User, s.Password), Path: "/postgres",
		RawQuery: url.Values{"host": {s.SocketDir}, "sslmode": {"disable"}}.Encode()}
	return u.String()
}

// Stop shuts the server down (fast shutdown: active transactions roll back).
func (s *Server) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-s.done:
	case <-time.After(20 * time.Second):
		_ = s.cmd.Process.Kill()
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
