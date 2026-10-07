package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Provider describes a model provider the owner can choose.
type Provider struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	NeedsKey     bool   `json:"needs_key"`
	NeedsBaseURL bool   `json:"needs_base_url"`
	DefaultModel string `json:"default_model"`
	KeysURL      string `json:"keys_url,omitempty"`
	// KeyEnv is the variable the owner passes the key in (seed run -e KEY_ENV).
	KeyEnv string `json:"key_env"`
}

// Providers lists the providers offered to owners. v0.1 starts with
// OpenRouter: one key gives access to any model. The Anthropic and
// OpenAI-compatible implementations exist and can be listed here later.
var Providers = []Provider{
	{ID: "openrouter", Label: "OpenRouter", NeedsKey: true, DefaultModel: "anthropic/claude-sonnet-5.5", KeysURL: "https://openrouter.ai/keys", KeyEnv: "OPENROUTER_API_KEY"},
}

func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Selection is a concrete choice of provider, model and credentials.
type Selection struct {
	Provider  string
	Name      string
	APIKey    string
	BaseURL   string
	MaxTokens int
}

// Build constructs the model for a selection (with retries).
func Build(s Selection) (Model, error) {
	maxTokens := s.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16000
	}
	if strings.TrimSpace(s.Name) == "" {
		return nil, fmt.Errorf("choose a model")
	}
	var m Model
	switch s.Provider {
	case "openrouter":
		base := s.BaseURL
		if base == "" {
			base = "https://openrouter.ai/api/v1"
		}
		m = &OpenAI{APIKey: s.APIKey, Model: s.Name, BaseURL: base, MaxTokens: maxTokens, Provider: "openrouter",
			PromptCaching: strings.HasPrefix(s.Name, "anthropic/")}
	case "anthropic":
		m = &Anthropic{APIKey: s.APIKey, Model: s.Name, BaseURL: s.BaseURL, MaxTokens: maxTokens}
	case "openai", "openai-compatible":
		base := s.BaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		m = &OpenAI{APIKey: s.APIKey, Model: s.Name, BaseURL: base, MaxTokens: maxTokens, Provider: s.Provider}
	default:
		return nil, fmt.Errorf("unknown provider %q", s.Provider)
	}
	if s.APIKey == "" && s.Provider != "openai-compatible" {
		return nil, fmt.Errorf("an API key is required")
	}
	return WithRetry(m, 5), nil
}

// Probe makes a minimal call to check that a model works with the given key.
func Probe(ctx context.Context, m Model) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Unwrap retries: a bad key should fail fast.
	if r, ok := m.(*retrying); ok {
		m = r.inner
	}
	_, err := m.Generate(ctx, Request{
		Messages:  []Message{{Role: User, Content: "Reply with the single word: ok"}},
		MaxTokens: 16,
	})
	return err
}

// Option is a model offered by a provider.
type Option struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length,omitempty"`
	Description   string `json:"description,omitempty"`
}

var optionsCache struct {
	sync.Mutex
	at   time.Time
	list []Option
}

// OpenRouterModels lists OpenRouter models that support tool calling (the
// Seed acts only through tools). The public list needs no key; it is cached.
func OpenRouterModels(ctx context.Context) ([]Option, error) {
	optionsCache.Lock()
	defer optionsCache.Unlock()
	if time.Since(optionsCache.at) < 10*time.Minute && len(optionsCache.list) > 0 {
		return optionsCache.list, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/models", nil)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openrouter: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			ContextLength       int      `json:"context_length"`
			SupportedParameters []string `json:"supported_parameters"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	var out []Option
	for _, m := range body.Data {
		tools := false
		for _, p := range m.SupportedParameters {
			if p == "tools" {
				tools = true
			}
		}
		if !tools || strings.HasSuffix(m.ID, ":batch") {
			continue
		}
		out = append(out, Option{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	optionsCache.at, optionsCache.list = time.Now(), out
	return out, nil
}

// Switchable is a Model whose implementation can be replaced at runtime when
// the owner picks another model.
type Switchable struct {
	mu    sync.RWMutex
	model Model
	info  Info
}

func NewSwitchable() *Switchable {
	return &Switchable{model: unavailable{"I have no mind yet: choose a model in my control plane"}}
}

func (s *Switchable) Set(m Model, info Info) {
	s.mu.Lock()
	s.model, s.info = m, info
	s.mu.Unlock()
}

func (s *Switchable) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.info
}

func (s *Switchable) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model.Name()
}

func (s *Switchable) Generate(ctx context.Context, req Request) (*Response, error) {
	s.mu.RLock()
	m := s.model
	s.mu.RUnlock()
	return m.Generate(ctx, req)
}
