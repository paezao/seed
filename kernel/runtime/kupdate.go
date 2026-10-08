package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"seed/kernel/fsx"
	"seed/kernel/git"
	"seed/kernel/memory"
	"seed/kernel/template"
	"seed/kernel/update"
)

// KernelUpdates brings me new kernels from signed releases (see the update
// package): it checks for them, applies one when my owner says so (as a new
// generation, then restarts me into it), and reports how it went. If a new
// kernel fails to build or start, the boot script rolls me back to the last
// kernel that ran well (markGood), and I report that too.
type KernelUpdates struct {
	Root    string
	Store   *memory.Store
	Bus     interface{ Publish(string, any) }
	Client  *update.Client
	Busy    func() bool // an evolution is running
	Restart func()

	mu        sync.Mutex
	latest    *update.Manifest
	checked   time.Time
	checkErr  string
	applying  bool
	lastEvent *KernelEvent
}

// KernelEvent is the last thing that happened to my kernel, for my owner.
type KernelEvent struct {
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
	Log     string    `json:"log,omitempty"`
	At      time.Time `json:"at"`
}

// KernelStatus is what the control plane shows.
type KernelStatus struct {
	Version      string           `json:"version"`
	Latest       *update.Manifest `json:"latest"`
	Available    bool             `json:"available"`
	NeedsRuntime bool             `json:"needs_runtime"`
	CheckedAt    *time.Time       `json:"checked_at"`
	CheckError   string           `json:"check_error,omitempty"`
	Applying     bool             `json:"applying"`
	LastEvent    *KernelEvent     `json:"last_event"`
}

const (
	settingInstalledAt = "kernel_release_published_at"
	settingPending     = "kernel_update_pending"
	settingFailed      = "kernel_update_failed"
	checkEvery         = 6 * time.Hour
	goodAfter          = 30 * time.Second
)

// Start reports what happened at the last restart, marks this kernel good
// once it has run well, and checks for releases periodically.
func (u *KernelUpdates) Start(ctx context.Context) {
	u.reportBoot(ctx)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(goodAfter):
			if err := u.markGood(ctx); err != nil {
				slog.Warn("marking this kernel good", "err", err)
			}
		}
	}()
	if u.Client == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	for {
		_, _ = u.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(checkEvery):
		}
	}
}

// markGood records this kernel as one that runs well: its commit, and a copy
// of its binary for the boot script to roll back with.
func (u *KernelUpdates) markGood(ctx context.Context) error {
	head, err := git.Open(u.Root).Head(ctx)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(u.Root, ".seed", "bin")
	if err := copyFile(exe, filepath.Join(bin, "seed.good.tmp")); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(bin, "seed.good.tmp"), filepath.Join(bin, "seed.good")); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(u.Root, ".seed", "kernel-good"), []byte(head+"\n"), 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// reportBoot tells my owner how the last kernel change went.
func (u *KernelUpdates) reportBoot(ctx context.Context) {
	current := template.SeedVersion(u.Root)
	rb := filepath.Join(u.Root, ".seed", "kernel-rollback.json")
	if b, err := os.ReadFile(rb); err == nil {
		var r struct {
			From, To, Reason, Log string
			Generation            int
		}
		_ = json.Unmarshal(b, &r)
		msg := fmt.Sprintf("My new kernel %s %s, so I went back to %s (generation %d). Nothing else changed.", r.From, r.Reason, r.To, r.Generation)
		if pending, _ := u.Store.Setting(ctx, settingPending); pending != "" {
			_ = u.Store.SetSetting(ctx, settingFailed, pending)
			_ = u.Store.SetSetting(ctx, settingPending, "")
		}
		u.event(ctx, false, msg, r.Log)
		_ = os.Remove(rb)
		return
	}
	pending, _ := u.Store.Setting(ctx, settingPending)
	if pending == "" {
		return
	}
	_ = u.Store.SetSetting(ctx, settingPending, "")
	if pending == current {
		u.event(ctx, true, "I'm now running kernel "+current+".", "")
	}
}

func (u *KernelUpdates) event(ctx context.Context, ok bool, msg, log string) {
	u.mu.Lock()
	u.lastEvent = &KernelEvent{OK: ok, Message: msg, Log: log, At: time.Now()}
	u.mu.Unlock()
	if m, err := u.Store.AddReport(ctx, memory.DefaultConversation, msg, ""); err == nil {
		u.Bus.Publish("message", m)
	}
	u.Bus.Publish("kernel", u.Status(ctx))
}

func (u *KernelUpdates) installedAt(ctx context.Context) time.Time {
	v, _ := u.Store.Setting(ctx, settingInstalledAt)
	t, _ := time.Parse(time.RFC3339, v)
	return t
}

func (u *KernelUpdates) needsRuntime(m *update.Manifest) bool {
	df, err := fsx.ReadFile(u.Root, "Dockerfile")
	return err != nil || update.RuntimeHash(df) != m.Runtime
}

// Status describes my kernel and what's available.
func (u *KernelUpdates) Status(ctx context.Context) KernelStatus {
	u.mu.Lock()
	s := KernelStatus{Version: template.SeedVersion(u.Root), Latest: u.latest, CheckError: u.checkErr, Applying: u.applying, LastEvent: u.lastEvent}
	if !u.checked.IsZero() {
		t := u.checked
		s.CheckedAt = &t
	}
	u.mu.Unlock()
	if s.Latest != nil {
		failed, _ := u.Store.Setting(ctx, settingFailed)
		s.Available = update.Newer(s.Latest, s.Version, u.installedAt(ctx)) && failed != s.Latest.Version
		s.NeedsRuntime = s.Available && u.needsRuntime(s.Latest)
	}
	return s
}

// Check looks for the latest release.
func (u *KernelUpdates) Check(ctx context.Context) (KernelStatus, error) {
	if u.Client == nil {
		return u.Status(ctx), errors.New("updates are off")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	m, err := u.Client.Latest(ctx)
	u.mu.Lock()
	u.checked = time.Now()
	if err != nil {
		u.checkErr = err.Error()
	} else {
		u.latest, u.checkErr = m, ""
	}
	u.mu.Unlock()
	s := u.Status(ctx)
	u.Bus.Publish("kernel", s)
	return s, err
}

var errBusyEvolving = errors.New("I'm evolving right now; update me when that is done")

// Apply installs the latest release as a new generation and restarts me
// into it. force replaces kernel changes the Seed made itself.
func (u *KernelUpdates) Apply(ctx context.Context, force bool) (*template.UpgradeResult, error) {
	s := u.Status(ctx)
	switch {
	case s.Latest == nil || !s.Available:
		return nil, errors.New("there is no newer kernel to install")
	case s.NeedsRuntime:
		return nil, errors.New("this kernel needs a new runtime image: restart me with `seed stop && seed upgrade && seed run` (or redeploy the new image)")
	case u.Busy != nil && u.Busy():
		return nil, errBusyEvolving
	}
	u.mu.Lock()
	if u.applying {
		u.mu.Unlock()
		return nil, errors.New("already updating")
	}
	u.applying = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.applying = false
		u.mu.Unlock()
	}()
	u.Bus.Publish("kernel", u.Status(ctx))
	m := s.Latest
	archive, err := u.Client.Template(ctx, m)
	if err != nil {
		return nil, err
	}
	res, err := template.Upgrade(ctx, u.Root, force, archive)
	if err != nil {
		return nil, err
	}
	if res.UpToDate {
		return res, nil
	}
	_ = u.Store.SetSetting(ctx, settingInstalledAt, m.PublishedAt.Format(time.RFC3339))
	_ = u.Store.SetSetting(ctx, settingPending, res.To)
	_ = u.Store.SetSetting(ctx, settingFailed, "")
	slog.Info("installed a new kernel; restarting into it", "from", res.From, "to", res.To, "generation", res.Generation)
	if u.Restart != nil {
		u.Restart()
	}
	return res, nil
}
