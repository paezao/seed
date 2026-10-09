import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, type Incident } from '../api';
import { Markdown } from '../components/Markdown';
import { Badge, Empty, ErrorNote, Loading, PageHeader, relTime, useLoad, type Tone } from '../components/ui';
import { useLiveEvent } from '../live';

const STATUS: Record<Incident['status'], { label: string; tone: Tone }> = {
  open: { label: 'new', tone: 'warn' },
  diagnosing: { label: 'investigating', tone: 'active' },
  diagnosed: { label: 'diagnosed', tone: 'warn' },
  fixing: { label: 'fixing', tone: 'active' },
  watching: { label: 'fixed, watching', tone: 'info' },
  resolved: { label: 'resolved', tone: 'ok' },
  ignored: { label: 'ignored', tone: 'neutral' },
};
const KIND: Record<Incident['kind'], string> = { http: 'errors', crash: 'crash', job: 'job' };

function IncidentCard({ inc, onChange }: { inc: Incident; onChange: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [showEvidence, setShowEvidence] = useState(false);
  const act = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    setErr(null);
    try { await fn(); onChange(); } catch (e) { setErr(errorMessage(e)); } finally { setBusy(null); }
  };
  const st = STATUS[inc.status];
  const done = inc.status === 'resolved' || inc.status === 'ignored';
  return (
    <li className={`incident incident-${inc.status}`}>
      <div className="incident-head">
        <Badge tone={st.tone} pulse={inc.status === 'diagnosing' || inc.status === 'fixing'}>{st.label}</Badge>
        <span className="incident-title">{inc.title}</span>
        <span className="spacer" />
        <span className="muted small">{KIND[inc.kind]} · {inc.count}× · last {relTime(inc.last_seen)}</span>
      </div>
      {inc.status === 'diagnosing' && <p className="small muted">I'm looking into it: reading my code, logs and data.</p>}
      {inc.diagnosis && <div className="incident-diagnosis"><Markdown source={inc.diagnosis} /></div>}
      {inc.fix && !done && <p className="incident-fix small"><strong>Fix:</strong> {inc.fix}</p>}
      {inc.note && <p className="small muted">{inc.note}</p>}
      {inc.evolution_id && <Link className="small link-quiet" to={`/evolutions/${inc.evolution_id}`}>The fix evolution →</Link>}
      {!done && (
        <div className="incident-actions">
          {(inc.status === 'diagnosed' || inc.status === 'open') && inc.diagnosis && (
            <button className="btn btn-sm btn-primary" disabled={busy !== null} onClick={() => act('fix', () => api.fixIncident(inc.id))}>
              {busy === 'fix' ? 'Starting…' : 'Fix it'}
            </button>
          )}
          {(inc.status === 'open' || inc.status === 'diagnosed') && (
            <button className="btn btn-sm" disabled={busy !== null} onClick={() => act('diag', () => api.diagnoseIncident(inc.id))}>
              {busy === 'diag' ? 'Starting…' : inc.diagnosis ? 'Investigate again' : 'Investigate'}
            </button>
          )}
          <button className="btn btn-sm btn-ghost" onClick={() => setShowEvidence((s) => !s)}>{showEvidence ? 'Hide evidence' : 'Evidence'}</button>
          <span className="spacer" />
          {inc.status !== 'fixing' && (
            <button className="btn btn-sm btn-ghost" disabled={busy !== null} onClick={() => act('ignore', () => api.ignoreIncident(inc.id))}
              title="Stop tracking this problem">Ignore</button>
          )}
        </div>
      )}
      {showEvidence && <pre className="incident-evidence mono small">{inc.evidence}</pre>}
      {err && <p className="error-text small">{err}</p>}
    </li>
  );
}

function AutoFix() {
  const load = useLoad(() => api.healthSettings(), []);
  const [busy, setBusy] = useState(false);
  const on = load.data?.auto_fix ?? false;
  const toggle = async () => {
    setBusy(true);
    try { load.setData(await api.setHealthSettings(!on)); } finally { setBusy(false); }
  };
  return (
    <label className="switch-row health-autofix">
      <input type="checkbox" role="switch" checked={on} disabled={busy || !load.data} onChange={toggle} />
      <span>
        <span className="switch-title">Fix problems on my own</span>
        <span className="small muted">
          When I've found the cause, I start the fix myself instead of waiting for you. Every fix is still an evolution: tested
          before it goes live, and a generation you can roll back.
        </span>
      </span>
    </label>
  );
}

export default function Health() {
  const load = useLoad(() => api.incidents(), []);
  useLiveEvent('incident', () => load.reload());
  const list = load.data ?? [];
  const current = list.filter((i) => i.status !== 'resolved' && i.status !== 'ignored');
  const past = list.filter((i) => i.status === 'resolved' || i.status === 'ignored');
  return (
    <div className="page">
      <PageHeader title="Health" sub="What goes wrong in my app, and what I do about it." />
      <section className="panel">
        <p className="small muted health-intro">
          I watch my app: runs of server errors, crashes (I restart it right away) and jobs that keep failing. When something
          goes wrong, I investigate on my own, tell you what I found, and fix it when you say so.
        </p>
        <AutoFix />
      </section>
      {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading && <Loading />}
      {load.data && current.length === 0 && <Empty title="All healthy">Nothing is going wrong right now.</Empty>}
      {current.length > 0 && <ul className="incidents">{current.map((i) => <IncidentCard key={i.id} inc={i} onChange={load.reload} />)}</ul>}
      {past.length > 0 && (
        <details className="incidents-past">
          <summary className="small muted">Past problems ({past.length})</summary>
          <ul className="incidents">{past.map((i) => <IncidentCard key={i.id} inc={i} onChange={load.reload} />)}</ul>
        </details>
      )}
    </div>
  );
}
