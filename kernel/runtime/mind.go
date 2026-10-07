package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"seed/kernel/models"
)

// The Seed's "mind": which model it thinks with.
//
// The Seed stores no credentials. The key arrives with the owner's secrets
// at start (OPENROUTER_API_KEY), or is lent in the control plane for the
// current session only. Either way it lives in memory. The model choice is
// not a secret: it is kept in operational memory (or passed as SEED_MODEL).

// sessionKeys are keys lent in the control plane, per provider, until restart.
var sessionKeys = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// providerKey returns the key for a provider and where it came from. A key
// lent in the control plane wins for the session (the owner chose it
// explicitly); otherwise the key passed at start.
func (k *Kernel) providerKey(p models.Provider) (key, source string) {
	sessionKeys.Lock()
	v := sessionKeys.m[p.ID]
	sessionKeys.Unlock()
	if v != "" {
		return v, "session"
	}
	if v := k.Secrets.Get(p.KeyEnv); v != "" {
		return v, "env"
	}
	return "", ""
}

type providerInfo struct {
	models.Provider
	HasKey    bool   `json:"has_key"`
	KeySource string `json:"key_source"`
	BaseURL   string `json:"base_url"`
}

type modelConfig struct {
	Provider   string         `json:"provider"`
	Name       string         `json:"name"`
	Configured bool           `json:"configured"`
	Providers  []providerInfo `json:"providers"`
}

func (k *Kernel) modelConfig() modelConfig {
	info := k.Mind.Info()
	mc := modelConfig{Provider: info.Provider, Name: info.Name, Configured: info.Configured}
	for _, p := range models.Providers {
		_, src := k.providerKey(p)
		mc.Providers = append(mc.Providers, providerInfo{Provider: p, HasKey: src != "", KeySource: src})
	}
	if mc.Provider == "" && len(models.Providers) > 0 {
		mc.Provider, mc.Name = models.Providers[0].ID, models.Providers[0].DefaultModel
	}
	return mc
}

// chosenModel returns this Seed's model choice: SEED_MODEL, then memory,
// then the provider's default.
func (k *Kernel) chosenModel(ctx context.Context) (models.Provider, string) {
	p := models.Providers[0]
	if id, err := k.Store.Setting(ctx, "model_provider"); err == nil {
		if pp, ok := models.ProviderByID(id); ok {
			p = pp
		}
	}
	name := p.DefaultModel
	if v, err := k.Store.Setting(ctx, "model_name"); err == nil && v != "" {
		name = v
	}
	if v := strings.TrimSpace(os.Getenv("SEED_MODEL")); v != "" {
		name = v
	}
	return p, name
}

// loadMind wires up the model at boot: with a key, I can think right away.
func (k *Kernel) loadMind(ctx context.Context) {
	p, name := k.chosenModel(ctx)
	key, _ := k.providerKey(p)
	if key == "" {
		k.Mind.Set(models.NewSwitchable(), models.Info{Provider: p.ID, Name: name})
		return
	}
	m, err := models.Build(models.Selection{Provider: p.ID, Name: name, APIKey: key, MaxTokens: k.Cfg.Model.MaxTokens})
	if err != nil {
		slog.Warn("my model is unavailable", "provider", p.ID, "model", name, "err", err)
		k.Mind.Set(models.NewSwitchable(), models.Info{Provider: p.ID, Name: name})
		return
	}
	k.Mind.Set(m, models.Info{Provider: p.ID, Name: name, Configured: true})
}

func (k *Kernel) handleModel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, k.modelConfig())
}

func (k *Kernel) handleModelOptions(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	p, ok := models.ProviderByID(provider)
	if !ok {
		writeErr(w, 400, fmt.Errorf("unknown provider %q", provider))
		return
	}
	var opts []models.Option
	var err error
	switch p.ID {
	case "openrouter":
		opts, err = models.OpenRouterModels(r.Context())
	}
	out := map[string]any{"models": opts}
	if opts == nil {
		out["models"] = []models.Option{{ID: p.DefaultModel, Name: p.DefaultModel}}
	}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, 200, out)
}

// handleSetModel switches model (and optionally lends a key for this
// session), validating with a tiny test call first.
func (k *Kernel) handleSetModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		APIKey   string `json:"api_key"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	p, ok := models.ProviderByID(body.Provider)
	if !ok {
		writeErr(w, 400, fmt.Errorf("unknown provider %q", body.Provider))
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		key, _ = k.providerKey(p)
	}
	if p.NeedsKey && key == "" {
		writeErr(w, 400, fmt.Errorf("I need a key: start me with `seed run -e %s`, or lend me one for this session", p.KeyEnv))
		return
	}
	// The endpoint is fixed by the provider: a caller cannot redirect the
	// test call (and the key) elsewhere.
	m, err := models.Build(models.Selection{Provider: p.ID, Name: body.Name, APIKey: key, MaxTokens: k.Cfg.Model.MaxTokens})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := models.Probe(r.Context(), m); err != nil {
		writeErr(w, 400, fmt.Errorf("%s could not use %s: %v", p.Label, body.Name, err))
		return
	}
	if strings.TrimSpace(body.APIKey) != "" {
		sessionKeys.Lock()
		sessionKeys.m[p.ID] = key
		sessionKeys.Unlock()
	}
	ctx := r.Context()
	if err := k.Store.SetSetting(ctx, "model_provider", p.ID); err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := k.Store.SetSetting(ctx, "model_name", body.Name); err != nil {
		writeErr(w, 500, err)
		return
	}
	k.Mind.Set(m, models.Info{Provider: p.ID, Name: body.Name, Configured: true})
	slog.Info("mind changed", "provider", p.ID, "model", body.Name)
	k.Bus.Publish("organism", nil) // triggers a status broadcast
	writeJSON(w, 200, k.modelConfig())
}

// handleForgetKey forgets a key lent for this session. Keys passed at start
// are the environment's; restart without them to remove them.
func (k *Kernel) handleForgetKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	p, ok := models.ProviderByID(body.Provider)
	if !ok {
		writeErr(w, 400, fmt.Errorf("unknown provider %q", body.Provider))
		return
	}
	if _, src := k.providerKey(p); src != "session" {
		writeErr(w, 400, errors.New("there is no lent key to forget; a key passed when I was started goes away when you restart me without it"))
		return
	}
	sessionKeys.Lock()
	delete(sessionKeys.m, p.ID)
	sessionKeys.Unlock()
	k.loadMind(r.Context())
	k.Bus.Publish("organism", nil)
	writeJSON(w, 200, k.modelConfig())
}
