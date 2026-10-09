package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"seed/kernel/events"
	"seed/kernel/ids"
	"seed/kernel/infra"
	"seed/kernel/memory"
)

// Backups keeps copies of my live data: before each generation goes live,
// once a day, and whenever my owner asks. Bringing one back stops my app,
// keeps a copy of the data of now (so a restore can be undone too),
// replaces the data, and lets the next deploy start the app again, applying
// any newer migrations to the older data.
//
// The files are pg_dump archives in .seed/backups, which no sandbox can
// see. They are taken and restored as my organism's own role, so they hold
// exactly what my app owns.
type Backups struct {
	Dir      string
	Store    *memory.Store
	Bus      *events.Bus
	Admin    infra.Admin
	Organism *Organism
	// Grants puts back what other roles may do in the live database (my
	// read-only role), which a fresh database doesn't have.
	Grants func(ctx context.Context) error

	mu  sync.Mutex // one backup or restore at a time
	now func() time.Time
}

const (
	settingDailyBackups = "daily_backups"
	backupTimeout       = 30 * time.Minute
)

// keep is how many copies of each kind I keep.
var keep = map[string]int{"before_generation": 10, "daily": 7, "manual": 10, "before_restore": 5}

// Available reports whether I have the tools to back up and restore.
func (b *Backups) Available() error {
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s isn't installed here", tool)
		}
	}
	return nil
}

func (b *Backups) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *Backups) current(ctx context.Context) int {
	if g, err := b.Store.CurrentGeneration(ctx); err == nil {
		return g.Number
	}
	return 0
}

// Take copies the live data now.
func (b *Backups) Take(ctx context.Context, kind, label string) (*memory.Backup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.take(ctx, kind, label)
}

func (b *Backups) take(ctx context.Context, kind, label string) (*memory.Backup, error) {
	if err := b.Available(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return nil, err
	}
	bk := &memory.Backup{ID: ids.New("bak"), Kind: kind, Generation: b.current(ctx), Label: label}
	bk.File = bk.ID + ".dump"
	path := filepath.Join(b.Dir, bk.File)
	tmp := path + ".partial"
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--file", tmp, b.Organism.DatabaseURL())
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("pg_dump: %v: %s", err, firstLine(string(out)))
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil {
		bk.Size = st.Size()
	}
	if err := b.Store.AddBackup(ctx, bk); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	b.prune(ctx, kind)
	b.Bus.Publish("backup", bk)
	slog.Info("backed up my data", "kind", kind, "generation", bk.Generation, "size", bk.Size)
	return bk, nil
}

// prune keeps the newest copies of a kind.
func (b *Backups) prune(ctx context.Context, kind string) {
	all, err := b.Store.Backups(ctx, kind)
	if err != nil {
		return
	}
	for i, bk := range all {
		if i < keep[kind] {
			continue
		}
		_ = os.Remove(filepath.Join(b.Dir, bk.File))
		_ = b.Store.DeleteBackup(ctx, bk.ID)
	}
}

// Path is where a backup's file is.
func (b *Backups) Path(bk *memory.Backup) string {
	return filepath.Join(b.Dir, filepath.Base(bk.File))
}

// BeforeLive copies the live data before another generation replaces it.
func (b *Backups) BeforeLive(ctx context.Context, e *memory.Evolution) error {
	if b.Available() != nil {
		return nil // nothing to copy with (a bare development setup)
	}
	label := "Before " + firstNonEmpty(e.Title, e.Intent)
	_, err := b.Take(ctx, "before_generation", truncate(label, 200))
	return err
}

// Restore replaces my live data with a backup; my app is left stopped.
func (b *Backups) Restore(ctx context.Context, id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bk, err := b.Store.Backup(ctx, id)
	if err != nil {
		return "", fmt.Errorf("backup %s: %w", id, err)
	}
	if _, err := os.Stat(b.Path(bk)); err != nil {
		return "", fmt.Errorf("the file of backup %s is missing", id)
	}
	// The data of now, so this restore can be undone.
	safety, err := b.take(ctx, "before_restore", "Before restoring the backup from "+bk.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if err != nil {
		return "", fmt.Errorf("I couldn't save the data of now first: %w", err)
	}
	b.Organism.StopForRestore(ctx)
	if err := b.replace(ctx, b.Path(bk)); err != nil {
		slog.Error("restoring a backup", "backup", id, "err", err)
		if perr := b.replace(ctx, b.Path(safety)); perr != nil {
			return safety.ID, fmt.Errorf("restoring failed (%v), and so did putting the data of now back (%v): it is in backup %s", err, perr, safety.ID)
		}
		return safety.ID, fmt.Errorf("restoring failed, so I put the data of now back: %w", err)
	}
	return safety.ID, nil
}

// replace makes the live database exactly a backup's data.
func (b *Backups) replace(ctx context.Context, file string) error {
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	db := b.Organism.dbName()
	if err := b.Admin.DropDatabase(ctx, db); err != nil {
		return err
	}
	if err := b.Admin.EnsureDatabase(ctx, db, b.Organism.Role); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "pg_restore", "--no-owner", "--no-privileges", "--exit-on-error", "--single-transaction",
		"--dbname", b.Organism.DatabaseURL(), file)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_restore: %v: %s", err, firstLine(string(out)))
	}
	if b.Grants != nil {
		if err := b.Grants(ctx); err != nil {
			return fmt.Errorf("restoring permissions: %w", err)
		}
	}
	return nil
}

// RestoreLive brings back a backup and starts my app on it again.
func (b *Backups) RestoreLive(ctx context.Context, id string) (string, error) {
	safety, err := b.Restore(ctx, id)
	if derr := b.Organism.Deploy(ctx); derr != nil && err == nil {
		err = fmt.Errorf("the data is restored, but my app didn't start on it: %w", derr)
	}
	return safety, err
}

// DailyOn reports whether I take a copy every day (on unless turned off).
func (b *Backups) DailyOn(ctx context.Context) bool {
	v, err := b.Store.Setting(ctx, settingDailyBackups)
	return err != nil || v != "off"
}

// Run takes the daily copy, checking every hour.
func (b *Backups) Run(ctx context.Context) {
	if b.Available() != nil {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	first := time.After(10 * time.Minute) // not while I'm starting
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
		case <-t.C:
		}
		b.daily(ctx)
	}
}

func (b *Backups) daily(ctx context.Context) {
	if !b.DailyOn(ctx) {
		return
	}
	if state, _ := b.Organism.State(); state != "running" {
		return
	}
	last, _ := b.Store.Backups(ctx, "daily")
	if len(last) > 0 && b.clock().Sub(last[0].CreatedAt) < 23*time.Hour {
		return
	}
	if _, err := b.Take(ctx, "daily", ""); err != nil {
		slog.Warn("daily backup", "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(s, 300)
}
