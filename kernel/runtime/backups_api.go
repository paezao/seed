package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"seed/kernel/memory"
)

func (k *Kernel) handleBackups(w http.ResponseWriter, r *http.Request) {
	list, err := k.Store.Backups(r.Context(), "")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*memory.Backup{}
	}
	unavailable := ""
	if err := k.Backups.Available(); err != nil {
		unavailable = err.Error()
	}
	writeJSON(w, 200, map[string]any{"backups": list, "daily": k.Backups.DailyOn(r.Context()), "unavailable": unavailable, "keep": keep})
}

// handleTakeBackup copies my live data now (my owner asked).
func (k *Kernel) handleTakeBackup(w http.ResponseWriter, r *http.Request) {
	bk, err := k.Backups.Take(context.WithoutCancel(r.Context()), "manual", "")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 201, bk)
}

func (k *Kernel) handleBackupSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Daily bool `json:"daily"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	v := "off"
	if body.Daily {
		v = "on"
	}
	if err := k.Store.SetSetting(r.Context(), settingDailyBackups, v); err != nil {
		writeErr(w, 500, err)
		return
	}
	k.handleBackups(w, r)
}

// handleRestoreBackup brings a backup back, in the background (it stops my
// app for a while); the chat says how it went.
func (k *Kernel) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	bk, err := k.Store.Backup(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, errors.New("no such backup"))
		return
	}
	if a := k.Orch.Active(r.Context()); a != nil && a.Status == memory.Applying {
		writeErr(w, 409, errors.New("a new generation is going live right now; try again in a moment"))
		return
	}
	ctx := context.WithoutCancel(r.Context())
	k.Bus.Publish("restore", map[string]string{"id": bk.ID, "state": "restoring"})
	go func() {
		when := bk.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")
		safety, err := k.Backups.RestoreLive(ctx, bk.ID)
		state, msg := "done", fmt.Sprintf("I brought back my data from the backup of **%s** (generation %d). The data as it was just before is kept in **Backups**, in case you want it back.", when, bk.Generation)
		if err != nil {
			slog.Error("restore", "backup", bk.ID, "err", err)
			state, msg = "failed", "I couldn't bring back the backup of **"+when+"**: "+err.Error()
			if safety != "" && !strings.Contains(err.Error(), safety) {
				msg += " The data as it was just before is in **Backups**."
			}
		}
		k.Bus.Publish("restore", map[string]string{"id": bk.ID, "state": state})
		if m, err := k.Store.AddReport(ctx, memory.DefaultConversation, msg, ""); err == nil {
			k.Bus.Publish("message", m)
		}
	}()
	writeJSON(w, 202, map[string]string{"state": "restoring"})
}

func (k *Kernel) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	bk, err := k.Store.Backup(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, errors.New("no such backup"))
		return
	}
	f, err := os.Open(k.Backups.Path(bk))
	if err != nil {
		writeErr(w, 404, errors.New("the backup's file is missing"))
		return
	}
	defer f.Close()
	name := fmt.Sprintf("%s-gen%d-%s.dump", strings.ReplaceAll(k.Cfg.Name, " ", "-"), bk.Generation, bk.CreatedAt.UTC().Format("20060102-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.Map(safeFileRune, name)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, bk.CreatedAt, f)
}

func safeFileRune(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		return r
	}
	return '-'
}
