import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';
import { api, errorMessage, type ModelConfig, type ModelOption, type ProviderInfo, type SetModelBody } from '../api';
import { useLive } from '../live';

/** "200000" -> "200K", "1048576" -> "1M". */
export function formatContext(n?: number): string {
  if (!n || n <= 0) return '';
  if (n >= 1_000_000) return `${+(n / 1_000_000).toFixed(n % 1_000_000 ? 1 : 0)}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return String(n);
}

type OptionsState = { provider: string; loading: boolean; models: ModelOption[]; error: string | null };

function useModelOptions(provider: string): OptionsState {
  const [state, setState] = useState<OptionsState>({ provider, loading: true, models: [], error: null });
  useEffect(() => {
    if (!provider) return;
    let cancelled = false;
    setState({ provider, loading: true, models: [], error: null });
    api.modelOptions(provider)
      .then((r) => { if (!cancelled) setState({ provider, loading: false, models: r.models ?? [], error: r.error || null }); })
      .catch((e) => { if (!cancelled) setState({ provider, loading: false, models: [], error: errorMessage(e) }); });
    return () => { cancelled = true; };
  }, [provider]);
  return state;
}

const MAX_SHOWN = 80;

/** A searchable model picker that also accepts any free-text model id. */
function ModelPicker({ id, value, onChange, options, loading, placeholder }: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  options: ModelOption[];
  loading: boolean;
  placeholder: string;
}) {
  const listId = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLUListElement>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return options;
    return options.filter((o) => o.id.toLowerCase().includes(q) || o.name.toLowerCase().includes(q));
  }, [options, query]);
  const shown = filtered.slice(0, MAX_SHOWN);
  const hasList = options.length > 0;

  useEffect(() => { setActive(0); }, [query, open]);
  useEffect(() => {
    if (!open) return;
    listRef.current?.querySelector<HTMLElement>(`[data-i="${active}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [active, open]);

  const choose = (o: ModelOption) => { onChange(o.id); setOpen(false); setQuery(''); };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (!hasList) return;
    if (e.key === 'ArrowDown') { e.preventDefault(); if (!open) setOpen(true); else setActive((a) => Math.min(a + 1, shown.length - 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
    else if (e.key === 'Enter' && open && shown[active]) { e.preventDefault(); choose(shown[active]); }
    else if (e.key === 'Escape' && open) { e.preventDefault(); setOpen(false); }
  };

  const selected = options.find((o) => o.id === value);

  return (
    <div className="combo">
      <input
        id={id}
        className="input mono"
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        role={hasList ? 'combobox' : undefined}
        aria-expanded={hasList ? open : undefined}
        aria-controls={hasList ? listId : undefined}
        aria-autocomplete={hasList ? 'list' : undefined}
        aria-activedescendant={hasList && open && shown[active] ? `${listId}-${active}` : undefined}
        onChange={(e) => { onChange(e.target.value); setQuery(e.target.value); setOpen(true); }}
        onFocus={() => { setQuery(''); if (hasList) setOpen(true); }}
        onClick={() => { if (hasList) setOpen(true); }}
        onBlur={() => setOpen(false)}
        onKeyDown={onKey}
      />
      <span className="combo-side" aria-hidden>
        {loading ? <span className="spinner" /> : selected?.context_length ? `${formatContext(selected.context_length)} ctx` : hasList ? '▾' : ''}
      </span>
      {open && hasList && (
        <ul className="combo-list" id={listId} role="listbox" ref={listRef} aria-label="Models">
          {shown.length === 0 && <li className="combo-empty">No match — I'll use “{value}” as typed.</li>}
          {shown.map((o, i) => (
            <li
              key={o.id}
              id={`${listId}-${i}`}
              data-i={i}
              role="option"
              aria-selected={o.id === value}
              className={`combo-opt${i === active ? ' active' : ''}${o.id === value ? ' selected' : ''}`}
              onMouseDown={(e) => { e.preventDefault(); choose(o); }}
              onMouseEnter={() => setActive(i)}
              title={o.description || undefined}
            >
              <span className="combo-opt-main">
                <span className="combo-opt-name truncate">{o.name || o.id}</span>
                {o.name && o.name !== o.id && <span className="combo-opt-id mono truncate">{o.id}</span>}
              </span>
              {o.context_length ? <span className="combo-opt-ctx mono">{formatContext(o.context_length)}</span> : null}
            </li>
          ))}
          {filtered.length > MAX_SHOWN && (
            <li className="combo-empty">{filtered.length - MAX_SHOWN} more — keep typing to narrow.</li>
          )}
        </ul>
      )}
    </div>
  );
}

/** Where the owner can get a key, per provider (data-driven; unknown providers get no link). */
const KEY_URLS: Record<string, string> = {
  openrouter: 'https://openrouter.ai/keys',
  anthropic: 'https://console.anthropic.com/settings/keys',
  openai: 'https://platform.openai.com/api-keys',
};

function GetKeyLink({ provider }: { provider: ProviderInfo }) {
  const href = KEY_URLS[provider.id];
  if (!href || !provider.needs_key) return null;
  return <a className="mf-get-key" href={href} target="_blank" rel="noreferrer">Get a key ↗</a>;
}

function initialProvider(config: ModelConfig): ProviderInfo | undefined {
  return config.providers.find((p) => p.id === config.provider) ?? config.providers[0];
}

type Props = {
  config: ModelConfig;
  /** Called with the kernel's new config after a successful change. */
  onSaved?: (cfg: ModelConfig) => void;
  submitLabel?: string;
  busyLabel?: string;
  onCancel?: () => void;
  cancelLabel?: string;
};

/** Choose provider, key, base URL and model; POST /model (the kernel test-calls the model). */
export function ModelForm({ config, onSaved, submitLabel = 'Save', busyLabel = 'Testing…', onCancel, cancelLabel = 'Cancel' }: Props) {
  const { refreshStatus } = useLive();
  const uid = useId();
  const start = initialProvider(config);
  const [providerId, setProviderId] = useState(start?.id ?? '');
  const provider = config.providers.find((p) => p.id === providerId);
  const [apiKey, setApiKey] = useState('');
  const [differentKey, setDifferentKey] = useState(false);
  const [baseUrl, setBaseUrl] = useState(start?.base_url ?? '');
  const [model, setModel] = useState(() =>
    start && start.id === config.provider && config.name ? config.name : start?.default_model ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const opts = useModelOptions(providerId);

  const pickProvider = (p: ProviderInfo) => {
    if (p.id === providerId) return;
    setProviderId(p.id);
    setApiKey('');
    setDifferentKey(false);
    setBaseUrl(p.base_url ?? '');
    setModel(p.id === config.provider && config.name ? config.name : p.default_model);
    setError(null);
  };

  const useStored = !!provider?.needs_key && provider.has_key && !differentKey;
  const keyMissing = !!provider?.needs_key && !useStored && !apiKey.trim();
  const urlMissing = !!provider?.needs_base_url && !baseUrl.trim();
  const canSubmit = !!provider && !!model.trim() && !keyMissing && !urlMissing && !busy;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!provider || !canSubmit) return;
    const body: SetModelBody = { provider: provider.id, name: model.trim() };
    if (provider.needs_key && !useStored) body.api_key = apiKey.trim();
    if (provider.needs_base_url) body.base_url = baseUrl.trim();
    setBusy(true);
    setError(null);
    try {
      const cfg = await api.setModel(body);
      setApiKey('');
      setDifferentKey(false);
      onSaved?.(cfg);
      await refreshStatus();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="model-form" onSubmit={submit} noValidate>
      {config.providers.length === 1 && provider ? (
        <div className="mf-provider-single">
          <span className="mf-label">Provider</span>
          <span className="provider-name">{provider.label}</span>
          <span className="spacer" />
          <GetKeyLink provider={provider} />
        </div>
      ) : (
        <fieldset className="mf-field" disabled={busy}>
          <legend className="mf-label">Provider</legend>
          <div className="provider-grid">
            {config.providers.map((p) => (
              <label key={p.id} className={`provider-card${p.id === providerId ? ' checked' : ''}`}>
                <input
                  type="radio"
                  name={`${uid}-provider`}
                  value={p.id}
                  checked={p.id === providerId}
                  onChange={() => pickProvider(p)}
                />
                <span className="provider-name">{p.label}</span>
                <span className="provider-note">
                  {!p.needs_key ? 'no key needed' : p.has_key ? <span className="ok-text">key stored</span> : 'needs a key'}
                </span>
              </label>
            ))}
          </div>
        </fieldset>
      )}

      {provider?.needs_key && (
        <div className="mf-field">
          {useStored ? (
            <>
              <span className="mf-label">API key</span>
              <div className="mf-stored">
                <span className="dot dot-ok" />
                <span>Using your stored {provider.label} key.</span>
                <button type="button" className="link-btn" onClick={() => setDifferentKey(true)} disabled={busy}>
                  Use a different key
                </button>
              </div>
            </>
          ) : (
            <>
              <div className="mf-label-row">
                <label className="mf-label" htmlFor={`${uid}-key`}>{provider.label} API key</label>
                {config.providers.length > 1 && <GetKeyLink provider={provider} />}
              </div>
              <input
                id={`${uid}-key`}
                className="input mono"
                type="password"
                autoComplete="off"
                spellCheck={false}
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="Paste your key"
                disabled={busy}
                autoFocus={differentKey}
              />
              {provider.has_key && (
                <button type="button" className="link-btn mf-under" onClick={() => { setDifferentKey(false); setApiKey(''); }} disabled={busy}>
                  Use my stored key instead
                </button>
              )}
            </>
          )}
        </div>
      )}

      {provider?.needs_base_url && (
        <div className="mf-field">
          <label className="mf-label" htmlFor={`${uid}-url`}>Base URL</label>
          <input
            id={`${uid}-url`}
            className="input mono"
            type="url"
            spellCheck={false}
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="http://localhost:11434/v1"
            disabled={busy}
          />
        </div>
      )}

      {provider && (
        <div className="mf-field">
          <label className="mf-label" htmlFor={`${uid}-model`}>
            Model
            {!opts.loading && opts.models.length > 0 && <span className="mf-label-note">{opts.models.length} available</span>}
          </label>
          <ModelPicker
            key={providerId}
            id={`${uid}-model`}
            value={model}
            onChange={setModel}
            options={opts.provider === providerId ? opts.models : []}
            loading={opts.loading}
            placeholder={provider.default_model || 'model id'}
          />
          {!opts.loading && opts.models.length === 0 && (
            <div className="mf-hint">
              {opts.error ? `Couldn't list models (${opts.error}). ` : ''}Type a model id
              {provider.default_model ? <> — e.g. <code>{provider.default_model}</code></> : null}.
            </div>
          )}
          <div className="mf-hint">Agentic work needs a strong tool-using model.</div>
        </div>
      )}

      {error && <div className="mf-error" role="alert">{error}</div>}

      <div className="mf-actions">
        {busy && <span className="mf-busy muted small"><span className="spinner" /> Making a tiny test call…</span>}
        <span className="spacer" />
        {onCancel && <button type="button" className="btn" onClick={onCancel} disabled={busy}>{cancelLabel}</button>}
        <button type="submit" className="btn btn-primary" disabled={!canSubmit}>{busy ? busyLabel : submitLabel}</button>
      </div>
    </form>
  );
}
