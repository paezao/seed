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

	"seed/kernel/infra"
	"seed/kernel/models"
)

// The Seed's "mind": which model it thinks with.
//
// A fresh Seed always asks its owner. Keys are stored per provider in the
// owner's user config (~/.config/seed/credentials.json, 0600), shared by all
// their Seeds and never written to a repository or returned by the API. The
// per-Seed choice of provider and model lives in operational memory.

const credentialsFile = "credentials.json"

type credential struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

type credentials struct {
	Providers map[string]credential `json:"providers"`
}

var credMu sync.Mutex

func loadCredentials() credentials {
	var c credentials
	if err := infra.ReadSecretJSON(credentialsFile, &c); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("reading credentials", "err", err)
	}
	if c.Providers == nil {
		c.Providers = map[string]credential{}
	}
	return c
}

func updateCredentials(fn func(*credentials)) error {
	credMu.Lock()
	defer credMu.Unlock()
	c := loadCredentials()
	fn(&c)
	return infra.WriteSecretJSON(credentialsFile, c)
}

type providerInfo struct {
	models.Provider
	HasKey  bool   `json:"has_key"`
	BaseURL string `json:"base_url"`
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
	creds := loadCredentials()
	for _, p := range models.Providers {
		c := creds.Providers[p.ID]
		mc.Providers = append(mc.Providers, providerInfo{Provider: p, HasKey: c.APIKey != "", BaseURL: c.BaseURL})
	}
	return mc
}

// loadMind restores this Seed's chosen model at boot.
func (k *Kernel) loadMind(ctx context.Context) {
	provider, _ := k.Store.Setting(ctx, "model_provider")
	name, _ := k.Store.Setting(ctx, "model_name")
	if provider == "" || name == "" {
		return
	}
	c := loadCredentials().Providers[provider]
	m, err := models.Build(models.Selection{Provider: provider, Name: name, APIKey: c.APIKey, BaseURL: c.BaseURL, MaxTokens: k.Cfg.Model.MaxTokens})
	if err != nil {
		slog.Warn("my chosen model is unavailable", "provider", provider, "model", name, "err", err)
		k.Mind.Set(models.NewSwitchable(), models.Info{Provider: provider, Name: name})
		return
	}
	k.Mind.Set(m, models.Info{Provider: provider, Name: name, Configured: true})
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

// handleSetModel validates a choice with a tiny test call, then stores the
// key (if given) and this Seed's choice, and switches immediately.
func (k *Kernel) handleSetModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
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
	body.APIKey = strings.TrimSpace(body.APIKey)
	stored := loadCredentials().Providers[p.ID]
	key, base := body.APIKey, body.BaseURL
	if key == "" {
		key = stored.APIKey
	}
	if base == "" {
		base = stored.BaseURL
	}
	if p.NeedsKey && key == "" {
		writeErr(w, 400, errors.New("enter your "+p.Label+" API key"))
		return
	}
	m, err := models.Build(models.Selection{Provider: p.ID, Name: body.Name, APIKey: key, BaseURL: base, MaxTokens: k.Cfg.Model.MaxTokens})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := models.Probe(r.Context(), m); err != nil {
		writeErr(w, 400, fmt.Errorf("%s could not use %s: %v", p.Label, body.Name, err))
		return
	}
	if body.APIKey != "" || body.BaseURL != "" {
		if err := updateCredentials(func(c *credentials) {
			cur := c.Providers[p.ID]
			if body.APIKey != "" {
				cur.APIKey = body.APIKey
			}
			if body.BaseURL != "" {
				cur.BaseURL = body.BaseURL
			}
			c.Providers[p.ID] = cur
		}); err != nil {
			writeErr(w, 500, err)
			return
		}
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

func (k *Kernel) handleForgetKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := updateCredentials(func(c *credentials) { delete(c.Providers, body.Provider) }); err != nil {
		writeErr(w, 500, err)
		return
	}
	if info := k.Mind.Info(); info.Provider == body.Provider {
		k.Mind.Set(models.NewSwitchable(), models.Info{Provider: info.Provider, Name: info.Name})
		k.Bus.Publish("organism", nil)
	}
	writeJSON(w, 200, k.modelConfig())
}
