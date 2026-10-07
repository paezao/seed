import { useState } from 'react';
import { api, errorMessage, type ModelConfig, type ProviderInfo } from '../api';
import { ModelForm } from '../components/ModelForm';
import { CopyCommand, keyEnvOf, keySourceText, restartCommand } from '../components/Brain';
import { Badge, ErrorNote, Loading, PageHeader, useLoad } from '../components/ui';
import { useLive } from '../live';

function Value({ v }: { v: unknown }) {
  if (v === null || v === undefined) return <span className="v-null">null</span>;
  if (typeof v === 'boolean') return <span className={v ? 'v-true' : 'v-false'}>{String(v)}</span>;
  if (typeof v === 'number') return <span className="v-num">{v}</span>;
  if (typeof v === 'string') return v === '' ? <span className="v-null">""</span> : <span className="v-str">{v}</span>;
  if (Array.isArray(v)) {
    if (v.length === 0) return <span className="v-null">[]</span>;
    if (v.every((x) => x === null || typeof x !== 'object')) {
      return <span className="v-list">{v.map((x, i) => <span key={i} className="chip"><Value v={x} /></span>)}</span>;
    }
    return <Tree obj={Object.fromEntries(v.map((x, i) => [String(i), x]))} />;
  }
  if (typeof v === 'object') {
    return Object.keys(v as object).length ? <Tree obj={v as Record<string, unknown>} /> : <span className="v-null">{'{}'}</span>;
  }
  return <span>{String(v)}</span>;
}

function Tree({ obj }: { obj: Record<string, unknown> }) {
  return (
    <dl className="tree">
      {Object.entries(obj).map(([k, v]) => {
        const nested = v !== null && typeof v === 'object' && !(Array.isArray(v) && v.every((x) => x === null || typeof x !== 'object'));
        return (
          <div key={k} className={`tree-row${nested ? ' nested' : ''}`}>
            <dt>{k}</dt>
            <dd><Value v={v} /></dd>
          </div>
        );
      })}
    </dl>
  );
}

function ForgetKey({ provider, onDone }: { provider: ProviderInfo; onDone: (cfg: ModelConfig) => void }) {
  const { refreshStatus } = useLive();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const go = async () => {
    setBusy(true);
    setErr(null);
    try {
      const cfg = await api.forgetKey(provider.id);
      setConfirming(false);
      onDone(cfg);
      await refreshStatus();
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (!confirming) {
    return (
      <button className="btn btn-sm btn-danger-ghost" onClick={() => setConfirming(true)} aria-label={`Forget the ${provider.label} key lent for this session`}>
        Forget key
      </button>
    );
  }
  return (
    <span className="inline-confirm">
      <span className="small">Forget the key you lent me? I can't think again until I get another.</span>
      <button className="btn btn-sm btn-danger" onClick={go} disabled={busy}>{busy ? 'Forgetting…' : 'Forget'}</button>
      <button className="btn btn-sm btn-ghost" onClick={() => { setConfirming(false); setErr(null); }} disabled={busy}>Cancel</button>
      {err && <span className="error-text small">{err}</span>}
    </span>
  );
}

function MindPanel() {
  const load = useLoad(() => api.model(), []);
  const [editing, setEditing] = useState(false);
  const cfg = load.data;
  const current = cfg?.providers.find((p) => p.id === cfg.provider);

  return (
    <section className="panel mind-panel" aria-labelledby="mind-h">
      <h2 id="mind-h">Mind</h2>
      {load.error && !cfg && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!cfg && load.loading && <Loading />}
      {cfg && (
        <>
          <div className="mind-current">
            <span className={`dot ${cfg.configured ? 'dot-ok' : 'dot-warn'}`} />
            {cfg.configured ? (
              <span>
                I think with <code className="fg">{cfg.name}</code>
                <span className="muted"> via {current?.label ?? cfg.provider}</span>
              </span>
            ) : (
              <span>I have no brain right now: no key was passed when I started.</span>
            )}
            <span className="spacer" />
            {cfg.configured && !editing && (
              <button className="btn btn-sm" onClick={() => setEditing(true)}>Change</button>
            )}
          </div>

          {!cfg.configured && (
            <div className="mind-restart">
              <p className="small muted">Restart me with a key from your environment:</p>
              <CopyCommand command={restartCommand(current)} label="Restart command" />
            </div>
          )}

          {(editing || !cfg.configured) && (
            <div className="mind-edit">
              {!cfg.configured && <p className="small muted mind-lend">Or lend me a key for this session:</p>}
              <ModelForm
                key={`${cfg.provider}/${cfg.name}/${cfg.configured}`}
                config={cfg}
                submitLabel={cfg.configured ? 'Switch' : 'Wake up'}
                busyLabel={cfg.configured ? 'Switching…' : 'Waking up…'}
                onSaved={(c) => { load.setData(c); setEditing(false); }}
                onCancel={cfg.configured ? () => setEditing(false) : undefined}
              />
            </div>
          )}

          <h3>Keys</h3>
          <p className="small muted">
            I keep no keys. Pass one when you start me (<code>seed run -e {keyEnvOf(current)}</code>); a key lent here
            lives in memory only, until I restart. Keys are never shown again.
          </p>
          <ul className="provider-list">
            {cfg.providers.map((p) => (
              <li key={p.id} className="provider-row">
                <span className="provider-row-name">{p.label}</span>
                {p.id === cfg.provider && cfg.configured && <Badge tone="info">in use</Badge>}
                {!p.needs_key
                  ? <span className="muted small mono truncate">{p.base_url || 'no key needed'}</span>
                  : p.has_key
                    ? <Badge tone={p.key_source === 'session' ? 'warn' : 'ok'}>{keySourceText(p)}</Badge>
                    : <span className="muted small">no key</span>}
                <span className="spacer" />
                {p.needs_key && p.key_source === 'session' && <ForgetKey provider={p} onDone={load.setData} />}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

export default function Settings() {
  const load = useLoad(() => api.settings(), []);
  return (
    <div className="page">
      <PageHeader title="Settings" />
      <MindPanel />
      <section className="panel" aria-labelledby="config-h">
        <h2 id="config-h">Configuration <span className="muted small" style={{ fontWeight: 400 }}>Read-only. Lives in <code>seed.yaml</code>; secrets are redacted.</span></h2>
        {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
        {!load.data && load.loading ? <Loading /> : load.data && <Tree obj={load.data} />}
      </section>
    </div>
  );
}
