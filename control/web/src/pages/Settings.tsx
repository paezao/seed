import { useState } from 'react';
import { api, errorMessage, type ModelConfig, type ProviderInfo } from '../api';
import { KernelPanel } from '../components/KernelUpdate';
import { NotificationsPanel } from '../components/Notifications';
import { Toggle } from '../components/Toggle';
import { ModelForm } from '../components/ModelForm';
import { CopyCommand, keyEnvOf, keySourceText, restartCommand } from '../components/Brain';
import { Badge, ErrorNote, Loading, PageHeader, relTime, useLoad } from '../components/ui';
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

const SECRET_NAME = /^[A-Z][A-Z0-9_]{1,63}$/;

function OutboundPanel() {
  const load = useLoad(() => api.outbound(), []);
  const [entry, setEntry] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const data = load.data;

  const act = async (key: string, fn: () => Promise<Awaited<ReturnType<typeof api.outbound>>>) => {
    setBusy(key);
    setErr(null);
    try {
      load.setData(await fn());
      return true;
    } catch (e) {
      setErr(errorMessage(e));
      return false;
    } finally {
      setBusy(null);
    }
  };
  const allow = (value: string) => {
    const v = value.trim();
    if (!v) return;
    const body = SECRET_NAME.test(v) ? { secrets: [v] } : { hosts: [v] };
    void act('add', () => api.grantOutbound(body)).then((ok) => { if (ok) setEntry(''); });
  };
  const allowedHosts = new Set(data?.hosts.map((h) => h.value) ?? []);

  return (
    <section className="panel" aria-labelledby="outbound-h">
      <h2 id="outbound-h">Outbound access</h2>
      <p className="small muted">
        My app can't reach the internet except over HTTPS to hosts you allow here, and it only sees the secrets you give it.
        When I need more while evolving, I'll ask you first. Tests never get secrets.
      </p>
      {load.error && !data && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!data && load.loading && <Loading />}
      {data && (
        <>
          <ul className="provider-list">
            {data.hosts.map((h) => (
              <li key={'h' + h.value} className="provider-row">
                <code className="fg">{h.value}</code>
                <span className="muted small truncate" title={h.reason}>{h.reason}</span>
                <span className="spacer" />
                <button className="btn btn-sm btn-danger-ghost" disabled={busy !== null} onClick={() => act('h' + h.value, () => api.revokeOutbound('host', h.value))}
                  aria-label={`Stop allowing ${h.value}`}>{busy === 'h' + h.value ? 'Removing…' : 'Remove'}</button>
              </li>
            ))}
            {data.secrets.map((s) => (
              <li key={'s' + s.value} className="provider-row">
                <Badge tone="warn">secret</Badge>
                <code className="fg">{s.value}</code>
                {!s.passed && <span className="muted small">not passed at this start</span>}
                <span className="muted small truncate" title={s.reason}>{s.reason}</span>
                <span className="spacer" />
                <button className="btn btn-sm btn-danger-ghost" disabled={busy !== null} onClick={() => act('s' + s.value, () => api.revokeOutbound('secret', s.value))}
                  aria-label={`Take ${s.value} away from the app`}>{busy === 's' + s.value ? 'Removing…' : 'Remove'}</button>
              </li>
            ))}
            {data.hosts.length + data.secrets.length === 0 && (
              <li className="provider-row muted small">Nothing yet: my app is fully offline.</li>
            )}
          </ul>
          <form className="outbound-add" onSubmit={(e) => { e.preventDefault(); allow(entry); }}>
            <label htmlFor="outbound-entry" className="small">Allow a host (<code>api.example.com</code>, <code>*.example.com</code>) or give a secret (<code>STRIPE_SECRET_KEY</code>)</label>
            <div className="outbound-add-row">
              <input id="outbound-entry" className="input" value={entry} onChange={(e) => setEntry(e.target.value)} placeholder="api.example.com" spellCheck={false} autoComplete="off" />
              <button className="btn btn-sm" type="submit" disabled={busy !== null || !entry.trim()}>{busy === 'add' ? 'Allowing…' : 'Allow'}</button>
            </div>
            {data.available_secrets.length > 0 && (
              <p className="small muted">Secrets you passed at start: {data.available_secrets.map((n, i) => <span key={n}>{i > 0 && ', '}<code>{n}</code></span>)}</p>
            )}
          </form>
          {err && <p className="error-text small">{err}</p>}
          {data.denied.length > 0 && (
            <>
              <h3>Recently refused</h3>
              <ul className="provider-list">
                {data.denied.slice(0, 8).map((d, i) => (
                  <li key={i} className="provider-row">
                    <code className="fg">{d.host || '(unknown)'}</code>
                    <span className="muted small truncate" title={d.reason}>{d.reason} · {relTime(d.at)}</span>
                    <span className="spacer" />
                    {d.host && !allowedHosts.has(d.host) && d.reason.includes('not allowed') && (
                      <button className="btn btn-sm" disabled={busy !== null} onClick={() => allow(d.host)}>Allow</button>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </section>
  );
}

function SignInLink() {
  const [link, setLink] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const make = async () => {
    setBusy(true);
    setErr(null);
    try {
      const { path } = await api.loginLink();
      setLink(window.location.origin + path);
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  if (link) {
    return (
      <div className="signin-link">
        <p className="small muted">Open this in the other browser. It works once, for 15 minutes; anyone who has it can sign in, so don't share it.</p>
        <CopyCommand command={link} label="Sign-in link" />
      </div>
    );
  }
  return (
    <>
      <button className="btn btn-sm" onClick={make} disabled={busy}>{busy ? 'Making a link…' : 'Sign in another browser'}</button>
      {err && <span className="error-text small"> {err}</span>}
    </>
  );
}

function BadgeSwitch() {
  const load = useLoad(() => api.badgeSetting(), []);
  return (
    <div className="badge-switch">
      <Toggle
        checked={load.data?.enabled ?? true}
        disabled={!load.data}
        title="Show a seed on my app's pages"
        on="A small seed in the corner of my app takes you back here. Only you see it, but my app's own code can tell when you're the one looking."
        off="Nothing is added to my app's pages. Turn it on for a shortcut back here."
        onChange={async (next) => { const r = await api.setBadge(next); load.setData(r); return r.enabled; }}
      />
    </div>
  );
}

function OwnerPanel() {
  const load = useLoad(() => api.ownerSessions(), []);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const signOut = async (s: { id: string; current: boolean }) => {
    setBusy(s.id);
    setErr(null);
    try {
      if (s.current) {
        await api.logout();
        window.location.assign(window.location.origin + '/_seed/');
        return;
      }
      await api.revokeSession(s.id);
      load.reload();
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="panel" aria-labelledby="owner-h">
      <h2 id="owner-h">Signed-in browsers</h2>
      <p className="small muted">
        Only you can come in here. A browser signs in with a one-time link: run <code>seed login</code> in my folder, or make one below.
      </p>
      {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading && <Loading />}
      {load.data && (
        <ul className="provider-list">
          {load.data.map((s) => (
            <li key={s.id} className="provider-row">
              <span className="provider-row-name">{s.label || 'a browser'}</span>
              {s.current && <Badge tone="info">this browser</Badge>}
              <span className="muted small">signed in {relTime(s.created_at)} · last here {relTime(s.last_seen_at)}</span>
              <span className="spacer" />
              <button className="btn btn-sm btn-danger-ghost" onClick={() => signOut(s)} disabled={busy !== null}
                aria-label={s.current ? 'Sign out of this browser' : `Sign out ${s.label}`}>
                {busy === s.id ? 'Signing out…' : 'Sign out'}
              </button>
            </li>
          ))}
          {load.data.length === 0 && <li className="provider-row muted small">You're using me through the CLI or a dev server.</li>}
        </ul>
      )}
      {err && <p className="error-text small">{err}</p>}
      <div className="owner-actions"><SignInLink /></div>
      <BadgeSwitch />
    </section>
  );
}

export default function Settings() {
  const load = useLoad(() => api.settings(), []);
  return (
    <div className="page">
      <PageHeader title="Settings" />
      <MindPanel />
      <KernelPanel />
      <OutboundPanel />
      <NotificationsPanel />
      <OwnerPanel />
      <section className="panel" aria-labelledby="config-h">
        <h2 id="config-h">Configuration <span className="muted small" style={{ fontWeight: 400 }}>Read-only. Lives in <code>seed.yaml</code>; secrets are redacted.</span></h2>
        {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
        {!load.data && load.loading ? <Loading /> : load.data && <Tree obj={load.data} />}
      </section>
    </div>
  );
}
