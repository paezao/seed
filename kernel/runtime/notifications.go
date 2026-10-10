package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"seed/kernel/events"
	"seed/kernel/knowledge"
	"seed/kernel/memory"
	"seed/kernel/notify"
)

// Notifications: I tell my owner's browsers (Web Push) when something needs
// them or something they'd want to know happened, even with no tab open.
// I watch my own events, so nothing else has to remember to notify.
//
// What a notification says goes through the push service encrypted; it
// opens a page of my control plane, nothing else.

const (
	settingVAPID       = "vapid_key"
	settingNotifyKinds = "notify_kinds"
)

// NotifyKind is something my owner can be notified about.
type NotifyKind struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	About string `json:"about"`
	On    bool   `json:"default"`
}

var notifyKinds = []NotifyKind{
	{"needs_you", "Needs you", "A change is ready to try, I have a question, or something waits for your approval.", true},
	{"evolutions", "Changes", "A new generation went live, or a change failed.", true},
	{"health", "Health", "I found a problem in my app.", true},
	{"budget", "Budget", "This month's budget is spent.", true},
	{"backups", "Backups", "A restore finished or failed.", true},
	{"updates", "Updates", "A new version of my kernel is available.", true},
	{"routines", "Routine reports", "A routine has something to tell you.", false},
}

// Notifier delivers notifications.
type Notifier struct {
	Store  *memory.Store
	Bus    *events.Bus
	Root   string
	Sender *notify.Sender

	mu     sync.Mutex
	signal map[string]string // what I last notified about each thing
}

// loadVAPID returns my push key, making one the first time.
func loadVAPID(ctx context.Context, store *memory.Store) (*notify.VAPID, error) {
	if v, err := store.Setting(ctx, settingVAPID); err == nil && v != "" {
		return notify.LoadVAPID(v)
	}
	k, err := notify.NewVAPID()
	if err != nil {
		return nil, err
	}
	return k, store.SetSetting(ctx, settingVAPID, k.Encode())
}

// Kinds says which kinds of notification are on.
func (n *Notifier) Kinds(ctx context.Context) map[string]bool {
	on := map[string]bool{}
	for _, k := range notifyKinds {
		on[k.Key] = k.On
	}
	if v, err := n.Store.Setting(ctx, settingNotifyKinds); err == nil && v != "" {
		var saved map[string]bool
		if json.Unmarshal([]byte(v), &saved) == nil {
			for k, b := range saved {
				if _, known := on[k]; known {
					on[k] = b
				}
			}
		}
	}
	return on
}

// Run watches my events and notifies.
func (n *Notifier) Run(ctx context.Context) {
	ch, cancel := n.Bus.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			kind, msg, ok := n.notificationFor(ev)
			if !ok || !n.Kinds(ctx)[kind] {
				continue
			}
			go n.Send(context.WithoutCancel(ctx), msg, urgencyOf(kind))
		}
	}
}

func urgencyOf(kind string) string {
	if kind == "needs_you" || kind == "health" {
		return "high"
	}
	return "normal"
}

// changed records what a thing is now; it reports whether that is new.
func (n *Notifier) changed(key, now string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.signal == nil {
		n.signal = map[string]string{}
	}
	if len(n.signal) > 5000 {
		n.signal = map[string]string{}
	}
	before := n.signal[key]
	n.signal[key] = now
	return now != "" && now != before
}

func (n *Notifier) name() string {
	if n.Root == "" {
		return "Seed"
	}
	return knowledge.Name(n.Root)
}

// notificationFor turns one of my events into a notification, if it is one.
func (n *Notifier) notificationFor(ev events.Event) (string, notify.Message, bool) {
	name := n.name()
	switch d := ev.Data.(type) {
	case *memory.Evolution:
		return n.forEvolution(name, d)
	case *memory.Approval:
		if d.Status == "pending" && n.changed("approval:"+d.ID, "pending") {
			return "needs_you", notify.Message{Title: name + ": may I?", Body: firstLine(d.Action), URL: "/_seed/", Tag: "approval-" + d.ID}, true
		}
	case *memory.Incident:
		if d.Status == "diagnosed" && n.changed("incident:"+d.ID, d.Status) {
			body := firstLine(d.Diagnosis)
			if d.Fix != "" {
				body += " I can fix it."
			}
			return "health", notify.Message{Title: name + ": " + d.Title, Body: body, URL: "/_seed/health", Tag: "incident-" + d.ID}, true
		}
	case *memory.Message:
		if d.Kind == "routine" && d.Role == "seed" {
			text := strings.TrimSpace(d.Content)
			title := name + ": routine report"
			if strings.HasPrefix(text, "**Routine · ") {
				if i := strings.Index(text[2:], "**"); i > 0 {
					title = name + ": " + strings.TrimPrefix(text[2:2+i], "Routine · ")
					text = strings.TrimSpace(text[4+i:])
				}
			}
			return "routines", notify.Message{Title: title, Body: firstLine(strings.ReplaceAll(text, "**", "")), URL: "/_seed/", Tag: "routine-" + d.ID}, true
		}
	case budgetSpent:
		if n.changed("budget", d.Month) {
			return "budget", notify.Message{Title: name + ": budget spent", Body: fmt.Sprintf("This month's budget of $%.2f is spent. I've paused what I do on my own.", d.Budget), URL: "/_seed/spending", Tag: "budget"}, true
		}
	case map[string]string:
		if ev.Type == "restore" && (d["state"] == "done" || d["state"] == "failed") && n.changed("restore:"+d["id"], d["state"]) {
			title, body := name+": data restored", "My app is running on the restored data."
			if d["state"] == "failed" {
				title, body = name+": restore failed", "The chat says why."
			}
			return "backups", notify.Message{Title: title, Body: body, URL: "/_seed/backups", Tag: "restore"}, true
		}
	case KernelStatus:
		if d.Available && d.Latest != nil && n.changed("kernel", d.Latest.Version) {
			return "updates", notify.Message{Title: name + ": update available", Body: "Kernel " + d.Latest.Version + " is ready to install.", URL: "/_seed/settings", Tag: "kernel"}, true
		}
	}
	return "", notify.Message{}, false
}

func (n *Notifier) forEvolution(name string, e *memory.Evolution) (string, notify.Message, bool) {
	title := firstNonEmpty(e.Title, firstLine(e.Intent))
	if e.Plan != nil && e.Plan.Title != "" {
		title = e.Plan.Title
	}
	url := "/_seed/evolutions/" + e.ID
	sig := ""
	switch {
	case e.Status == memory.Ready && e.Preview != nil && e.Preview.State == "ready":
		sig = "preview"
	case e.Status == memory.NeedsInput && len(e.Questions) > 0:
		sig = "question"
	case e.Status == memory.Complete || e.Status == memory.Failed || e.Status == memory.RolledBack:
		sig = string(e.Status)
	}
	if !n.changed("evolution:"+e.ID, sig) {
		return "", notify.Message{}, false
	}
	tag := "evolution-" + e.ID
	switch sig {
	case "preview":
		return "needs_you", notify.Message{Title: name + ": ready to try", Body: title, URL: url, Tag: tag}, true
	case "question":
		return "needs_you", notify.Message{Title: name + " has a question", Body: firstLine(e.Questions[0].Question), URL: "/_seed/", Tag: tag}, true
	case "complete":
		body := title
		if e.NewGeneration != nil {
			body = fmt.Sprintf("Generation %d: %s", *e.NewGeneration, title)
		}
		return "evolutions", notify.Message{Title: name + ": live", Body: body, URL: url, Tag: tag}, true
	default:
		return "evolutions", notify.Message{Title: name + ": couldn't finish", Body: title + ". " + firstLine(e.Error), URL: url, Tag: tag}, true
	}
}

// budgetSpent is published when this month's budget is spent.
type budgetSpent struct {
	Month  string  `json:"month"`
	Budget float64 `json:"budget_usd"`
}

// Send delivers a message to every subscribed browser; it returns how many
// got it.
func (n *Notifier) Send(ctx context.Context, msg notify.Message, urgency string) int {
	subs, err := n.Store.PushSubscriptions(ctx)
	if err != nil || n.Sender == nil {
		return 0
	}
	ok := 0
	for _, s := range subs {
		err := n.Sender.Send(ctx, notify.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}, msg, urgency)
		switch {
		case errors.Is(err, notify.ErrGone):
			_ = n.Store.DeletePushSubscription(ctx, s.ID)
		case err != nil:
			slog.Warn("notification", "subscription", s.ID, "err", err)
			_ = n.Store.MarkPush(ctx, s.ID, firstLine(err.Error()))
		default:
			ok++
			_ = n.Store.MarkPush(ctx, s.ID, "")
		}
	}
	return ok
}

// ---- API

func (k *Kernel) handleNotifications(w http.ResponseWriter, r *http.Request) {
	subs, err := k.Store.PushSubscriptions(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if subs == nil {
		subs = []*memory.PushSubscription{}
	}
	writeJSON(w, 200, map[string]any{
		"public_key": k.Notifier.Sender.VAPID.PublicKey(), "subscriptions": subs,
		"kinds": k.Notifier.Kinds(r.Context()), "about": notifyKinds,
	})
}

// handleSubscribe adds this browser.
func (k *Kernel) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var body notify.Subscription
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := notify.CheckSubscription(body); err != nil {
		writeErr(w, 400, err)
		return
	}
	p := &memory.PushSubscription{Endpoint: body.Endpoint, P256dh: body.P256dh, Auth: body.Auth, UserAgent: truncate(r.UserAgent(), 300)}
	if err := k.Store.SavePushSubscription(r.Context(), p); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 201, p)
}

func (k *Kernel) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := k.Store.DeletePushSubscription(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err)
		return
	}
	k.handleNotifications(w, r)
}

func (k *Kernel) handleNotifyKinds(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kinds map[string]bool `json:"kinds"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	on := k.Notifier.Kinds(r.Context())
	for key, v := range body.Kinds {
		if _, known := on[key]; known {
			on[key] = v
		}
	}
	b, _ := json.Marshal(on)
	if err := k.Store.SetSetting(r.Context(), settingNotifyKinds, string(b)); err != nil {
		writeErr(w, 500, err)
		return
	}
	k.handleNotifications(w, r)
}

// handleTestNotification sends a test to every subscribed browser.
func (k *Kernel) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	n := k.Notifier.Send(r.Context(), notify.Message{Title: k.Notifier.name() + ": notifications work", Body: "This is how I'll tell you when something needs you.", URL: "/_seed/settings", Tag: "test"}, "normal")
	if n == 0 {
		writeErr(w, 502, errors.New("no browser got it: see the devices below for why"))
		return
	}
	writeJSON(w, 200, map[string]int{"delivered": n})
}
