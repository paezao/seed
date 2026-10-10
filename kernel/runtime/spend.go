package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"seed/kernel/memory"
	"seed/kernel/models"
)

// Spending: every model call I make is written down with what it was for
// and what my provider charged (OpenRouter says, per call). My owner can
// set a monthly budget: once it is spent, I stop spending on my own
// (scheduled routines, investigating and fixing problems) until the next
// month or a bigger budget. What my owner asks for still happens: they
// see the spending, and it is their call.

const (
	settingBudget      = "monthly_budget_usd"
	settingBudgetNoted = "budget_noted"
	maxBudget          = 100000
)

var budgetNote sync.Mutex

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// meter records one model call.
func (k *Kernel) meter(ctx context.Context, info models.Info, u models.Usage) {
	p := models.PurposeOf(ctx)
	ctx = context.WithoutCancel(ctx)
	err := k.Store.AddSpend(ctx, memory.Spend{
		Kind: p.Kind, Ref: p.Ref, Model: info.Provider + "/" + info.Name,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens, CostUSD: u.CostUSD, Priced: u.Priced,
	})
	if err != nil {
		slog.Warn("recording spending", "err", err)
		return
	}
	k.noteBudget(ctx)
}

// budget is my owner's monthly budget in USD (0: none).
func (k *Kernel) budget(ctx context.Context) float64 {
	v, err := k.Store.Setting(ctx, settingBudget)
	if err != nil || v == "" {
		return 0
	}
	b, err := strconv.ParseFloat(v, 64)
	if err != nil || b < 0 {
		return 0
	}
	return b
}

// spendPaused says why I shouldn't spend on my own now, or "".
func (k *Kernel) spendPaused(ctx context.Context) string {
	b := k.budget(ctx)
	if b <= 0 {
		return ""
	}
	spent, err := k.Store.SpentSince(ctx, monthStart(time.Now()))
	if err != nil || spent < b {
		return ""
	}
	return fmt.Sprintf("Paused: this month's budget ($%.2f) is spent. Raise it in Spending, or I'll carry on next month.", b)
}

// noteBudget tells my owner, once a month (and again for a new budget),
// that the budget is spent.
func (k *Kernel) noteBudget(ctx context.Context) {
	if k.spendPaused(ctx) == "" {
		return
	}
	budgetNote.Lock()
	defer budgetNote.Unlock()
	b := k.budget(ctx)
	mark := monthStart(time.Now()).Format("2006-01") + "@" + strconv.FormatFloat(b, 'f', 2, 64)
	if v, _ := k.Store.Setting(ctx, settingBudgetNoted); v == mark {
		return
	}
	if err := k.Store.SetSetting(ctx, settingBudgetNoted, mark); err != nil {
		return
	}
	msg := fmt.Sprintf("I've reached this month's budget of **$%.2f**. Until next month, or until you raise it in **Spending**, "+
		"I won't run routines or investigate and fix problems on my own. Anything you ask me still happens.", b)
	if m, err := k.Store.AddReport(ctx, memory.DefaultConversation, msg, ""); err == nil {
		k.Bus.Publish("message", m)
	}
	k.Bus.Publish("organism", nil) // a status broadcast: the control plane shows it
	k.Bus.Publish("budget", budgetSpent{Month: monthStart(time.Now()).Format("2006-01"), Budget: b})
}

type spendStatus struct {
	MonthUSD  float64 `json:"month_usd"`
	BudgetUSD float64 `json:"budget_usd,omitempty"`
	Paused    bool    `json:"paused,omitempty"`
}

func (k *Kernel) spendStatus(ctx context.Context) *spendStatus {
	spent, err := k.Store.SpentSince(ctx, monthStart(time.Now()))
	if err != nil {
		return nil
	}
	s := &spendStatus{MonthUSD: spent, BudgetUSD: k.budget(ctx)}
	s.Paused = s.BudgetUSD > 0 && spent >= s.BudgetUSD
	return s
}

// handleSpending reports this month's spending.
func (k *Kernel) handleSpending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rep, err := k.Store.SpendReportSince(ctx, monthStart(time.Now()))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for i := range rep.Top {
		rep.Top[i].Label = k.spendLabel(ctx, rep.Top[i].Key, rep.Top[i].Ref)
	}
	st := k.spendStatus(ctx)
	writeJSON(w, 200, map[string]any{"month": rep, "budget_usd": st.BudgetUSD, "paused": st.Paused})
}

// spendLabel names what a spending reference is.
func (k *Kernel) spendLabel(ctx context.Context, kind, ref string) string {
	switch kind {
	case "evolution":
		if e, err := k.Store.Evolution(ctx, ref); err == nil {
			return firstNonEmpty(e.Title, e.Intent)
		}
	case "routine":
		if rt, err := k.Store.Routine(ctx, ref); err == nil {
			return rt.Name
		}
	case "health":
		if inc, err := k.Store.Incident(ctx, ref); err == nil {
			return inc.Title
		}
	}
	return ""
}

// handleSetBudget sets (or, with 0, removes) the monthly budget.
func (k *Kernel) handleSetBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if math.IsNaN(body.BudgetUSD) || body.BudgetUSD < 0 || body.BudgetUSD > maxBudget {
		writeErr(w, 400, errors.New("the budget must be between $0 (none) and $100,000"))
		return
	}
	v := ""
	if body.BudgetUSD > 0 {
		v = strconv.FormatFloat(math.Round(body.BudgetUSD*100)/100, 'f', 2, 64)
	}
	if err := k.Store.SetSetting(r.Context(), settingBudget, v); err != nil {
		writeErr(w, 500, err)
		return
	}
	k.Bus.Publish("organism", nil)
	k.handleSpending(w, r)
}
