import { useState } from 'react';
import { api, errorMessage, type NewRoutine, type Routine, type RoutineRun } from '../api';
import { Markdown } from '../components/Markdown';
import { Badge, Empty, ErrorNote, Loading, PageHeader, relTime, useLoad } from '../components/ui';
import { useLiveEvent } from '../live';

const TZ = (() => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
})();

const PRESETS: { label: string; schedule: string; agent: boolean }[] = [
  { label: 'Every 5 minutes', schedule: 'every 5m', agent: false },
  { label: 'Every 15 minutes', schedule: 'every 15m', agent: true },
  { label: 'Every hour', schedule: 'every 1h', agent: true },
  { label: 'Every day at 08:00', schedule: '0 8 * * *', agent: true },
  { label: 'Weekdays at 18:00', schedule: '0 18 * * 1-5', agent: true },
  { label: 'Mondays at 09:00', schedule: '0 9 * * 1', agent: true },
];

function RunStatus({ run }: { run: RoutineRun | null }) {
  if (!run) return <span className="muted small">never ran</span>;
  const tone = run.status === 'ok' ? 'ok' : run.status === 'failed' ? 'bad' : run.status === 'running' ? 'active' : 'neutral';
  const label = { ok: 'ran', quiet: 'nothing to report', failed: 'failed', running: 'running' }[run.status];
  return (
    <span className="routine-run-status">
      <Badge tone={tone} pulse={run.status === 'running'}>{label}</Badge>
      <span className="muted small">{relTime(run.started_at)}</span>
    </span>
  );
}

function Runs({ routine }: { routine: Routine }) {
  const load = useLoad(() => api.routineRuns(routine.id), [routine.id, routine.last_run?.id, routine.last_run?.status]);
  if (load.error) return <ErrorNote error={load.error} onRetry={load.reload} />;
  if (!load.data) return <Loading />;
  if (load.data.length === 0) return <p className="muted small">No runs yet.</p>;
  return (
    <ol className="routine-runs">
      {load.data.map((r) => (
        <li key={r.id}>
          <div className="routine-run-head">
            <RunStatus run={r} />
            {r.trigger === 'manual' && <span className="muted small">run by you</span>}
          </div>
          {r.output && r.status !== 'running' && (
            routine.kind === 'agent' && r.status === 'ok'
              ? <div className="routine-run-output"><Markdown source={r.output} /></div>
              : <pre className="routine-run-output mono small">{r.output}</pre>
          )}
        </li>
      ))}
    </ol>
  );
}

function RoutineRow({ r, onChange }: { r: Routine; onChange: () => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const act = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    setErr(null);
    try {
      await fn();
      onChange();
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const running = r.last_run?.status === 'running';

  return (
    <li className={`routine ${r.enabled ? '' : 'routine-paused'}`}>
      <div className="routine-head">
        <button className="routine-toggle" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          <span className="routine-name">{r.name}</span>
          <Badge tone={r.kind === 'agent' ? 'info' : 'neutral'}>{r.kind === 'agent' ? 'asks me' : `${r.method} ${r.path}`}</Badge>
          {r.source === 'organism' && <Badge>from my code</Badge>}
        </button>
        <span className="spacer" />
        <RunStatus run={r.last_run} />
      </div>
      <div className="routine-meta small muted">
        <span>{r.schedule_text}</span>
        {r.enabled && r.next_run_at && <span> · next {relTime(r.next_run_at)}</span>}
        {!r.enabled && <span> · paused</span>}
      </div>
      {r.kind === 'agent' && r.prompt && <p className="routine-prompt small">{r.prompt}</p>}
      {r.kind === 'job' && r.description && <p className="routine-prompt small">{r.description}</p>}
      <div className="routine-actions">
        <button className="btn btn-sm" disabled={busy !== null || running} onClick={() => act('run', () => api.runRoutine(r.id))}>
          {running ? 'Running…' : busy === 'run' ? 'Starting…' : 'Run now'}
        </button>
        <button className="btn btn-sm btn-ghost" disabled={busy !== null} onClick={() => act('toggle', () => api.updateRoutine(r.id, { enabled: !r.enabled }))}>
          {r.enabled ? 'Pause' : 'Resume'}
        </button>
        <button className="btn btn-sm btn-ghost" onClick={() => setOpen((o) => !o)}>{open ? 'Hide runs' : 'Runs'}</button>
        <span className="spacer" />
        {r.source === 'owner' && !confirmDelete && (
          <button className="btn btn-sm btn-danger-ghost" disabled={busy !== null} onClick={() => setConfirmDelete(true)} aria-label={`Delete ${r.name}`}>Delete</button>
        )}
        {confirmDelete && (
          <span className="inline-confirm">
            <span className="small">Delete {r.name}?</span>
            <button className="btn btn-sm btn-danger" disabled={busy !== null} onClick={() => act('delete', () => api.deleteRoutine(r.id))}>Delete</button>
            <button className="btn btn-sm btn-ghost" onClick={() => setConfirmDelete(false)}>Cancel</button>
          </span>
        )}
      </div>
      {err && <p className="error-text small">{err}</p>}
      {open && <Runs routine={r} />}
    </li>
  );
}

function NewRoutineForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [kind, setKind] = useState<'agent' | 'job'>('agent');
  const [name, setName] = useState('');
  const [schedule, setSchedule] = useState('0 8 * * *');
  const [prompt, setPrompt] = useState('');
  const [method, setMethod] = useState('POST');
  const [path, setPath] = useState('/internal/jobs/');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const body: NewRoutine = kind === 'agent'
      ? { kind, name: name.trim(), schedule, timezone: TZ, prompt }
      : { kind, name: name.trim(), schedule, timezone: TZ, method, path };
    try {
      await api.createRoutine(body);
      onDone();
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="panel routine-form" onSubmit={submit} aria-labelledby="new-routine-h">
      <h2 id="new-routine-h">New routine</h2>
      <div className="segmented" role="radiogroup" aria-label="What should happen">
        <button type="button" role="radio" aria-checked={kind === 'agent'} className={kind === 'agent' ? 'on' : ''} onClick={() => setKind('agent')}>Ask me to do something</button>
        <button type="button" role="radio" aria-checked={kind === 'job'} className={kind === 'job' ? 'on' : ''} onClick={() => setKind('job')}>Call my app</button>
      </div>
      <p className="small muted">
        {kind === 'agent'
          ? "I'll do it on my own and tell you in chat what I found (or stay quiet when there's nothing to say). Changing data still needs your approval."
          : 'I call one of my app’s endpoints, with a token my app can check. For new scheduled work, it’s usually better to ask me in chat: I’ll write the endpoint and its schedule.'}
      </p>
      <label className="field">
        <span>Name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]+/g, '-'))} placeholder={kind === 'agent' ? 'daily-summary' : 'send-reminders'} required />
      </label>
      {kind === 'agent' ? (
        <label className="field">
          <span>What should I do?</span>
          <textarea className="input textarea" rows={4} value={prompt} onChange={(e) => setPrompt(e.target.value)}
            placeholder="Tell me how many recipes were added yesterday, and which ones have no photo yet." required />
        </label>
      ) : (
        <div className="field-row">
          <label className="field field-narrow">
            <span>Method</span>
            <select className="input" value={method} onChange={(e) => setMethod(e.target.value)}>
              {['POST', 'GET', 'PUT', 'PATCH', 'DELETE'].map((m) => <option key={m}>{m}</option>)}
            </select>
          </label>
          <label className="field">
            <span>Path</span>
            <input className="input mono" value={path} onChange={(e) => setPath(e.target.value)} required />
          </label>
        </div>
      )}
      <fieldset className="field">
        <legend>When</legend>
        <div className="presets">
          {PRESETS.filter((p) => kind === 'job' || p.agent).map((p) => (
            <button type="button" key={p.schedule} className={`preset ${schedule === p.schedule ? 'on' : ''}`} onClick={() => setSchedule(p.schedule)}>{p.label}</button>
          ))}
        </div>
        <input className="input mono" value={schedule} onChange={(e) => setSchedule(e.target.value)} aria-label="Schedule"
          placeholder='every 2h, or cron like "30 7 * * 1-5"' />
        <span className="small muted">“every 15m”, “every 2h”, “every 1d”, or cron. Times are in {TZ}.</span>
      </fieldset>
      {err && <p className="error-text small">{err}</p>}
      <div className="routine-form-actions">
        <button className="btn btn-ghost" type="button" onClick={onCancel}>Cancel</button>
        <button className="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create routine'}</button>
      </div>
    </form>
  );
}

export default function Routines() {
  const load = useLoad(() => api.routines(), []);
  const [creating, setCreating] = useState(false);
  useLiveEvent('routine', () => load.reload());

  const list = load.data ?? [];
  return (
    <div className="page">
      <PageHeader
        title="Routines"
        sub="What I do on my own, on a schedule."
        actions={!creating && <button className="btn btn-primary btn-sm" onClick={() => setCreating(true)}>New routine</button>}
      />
      {creating && <NewRoutineForm onDone={() => { setCreating(false); load.reload(); }} onCancel={() => setCreating(false)} />}
      {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading && <Loading />}
      {load.data && list.length === 0 && !creating && (
        <Empty title="No routines yet">
          Ask me in chat, like “every morning, tell me what changed yesterday”, or create one here.
        </Empty>
      )}
      {list.length > 0 && (
        <ul className="routines">
          {list.map((r) => <RoutineRow key={r.id} r={r} onChange={load.reload} />)}
        </ul>
      )}
    </div>
  );
}
