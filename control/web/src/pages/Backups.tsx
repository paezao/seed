import { useEffect, useState } from 'react';
import { api, bytes, downloadBackup, errorMessage, type Backup, type BackupsInfo } from '../api';
import { Toggle } from '../components/Toggle';
import { Badge, Empty, ErrorNote, Loading, PageHeader, Time, absTime, useLoad } from '../components/ui';
import { useLiveEvent } from '../live';

const KIND: Record<Backup['kind'], string> = {
  before_generation: 'Before a new generation',
  daily: 'Daily',
  manual: 'Made by you',
  before_restore: 'Before a restore',
};

function describe(b: Backup): string {
  switch (b.kind) {
    case 'before_generation': return b.label || 'Before a new generation went live';
    case 'before_restore': return 'The data just before a restore replaced it';
    case 'daily': return 'Daily copy';
    default: return 'Copy you asked for';
  }
}

function BackupRow({ b, busy, onRestore }: { b: Backup; busy: boolean; onRestore: (b: Backup) => Promise<void> }) {
  const [confirming, setConfirming] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const download = async () => {
    setDownloading(true);
    setErr(null);
    try { await downloadBackup(b.id); } catch (e) { setErr(errorMessage(e)); } finally { setDownloading(false); }
  };
  return (
    <li className="backup">
      <div className="backup-main">
        <div className="backup-title">
          <Badge tone={b.kind === 'before_restore' ? 'warn' : b.kind === 'manual' ? 'info' : 'neutral'}>{KIND[b.kind]}</Badge>
          <span className="truncate" title={describe(b)}>{describe(b)}</span>
        </div>
        <div className="muted small">
          <span title={absTime(b.created_at)}><Time iso={b.created_at} /></span> · generation {b.generation} · {bytes(b.size)}
        </div>
        {confirming && (
          <div className="backup-confirm small">
            Replace my live data with this copy? Anything changed since <strong>{absTime(b.created_at)}</strong> is lost from the
            live data, but I keep a copy of the data as it is now first, so you can undo it. My app is down for a moment while I do it.
          </div>
        )}
        {err && <div className="error-text small">{err}</div>}
      </div>
      <div className="backup-actions">
        {confirming ? (
          <>
            <button type="button" className="btn btn-sm btn-danger" disabled={busy}
              onClick={async () => { try { await onRestore(b); setConfirming(false); } catch (e) { setErr(errorMessage(e)); } }}>
              {busy ? 'Restoring…' : 'Restore'}
            </button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => setConfirming(false)} disabled={busy}>Cancel</button>
          </>
        ) : (
          <>
            <button type="button" className="btn btn-sm" onClick={download} disabled={downloading}>{downloading ? 'Downloading…' : 'Download'}</button>
            <button type="button" className="btn btn-sm" onClick={() => { setErr(null); setConfirming(true); }} disabled={busy}>Restore…</button>
          </>
        )}
      </div>
    </li>
  );
}

export default function Backups() {
  const load = useLoad(() => api.backups(), []);
  const [info, setInfo] = useState<BackupsInfo | null>(null);
  const [taking, setTaking] = useState(false);
  const [restoring, setRestoring] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { if (load.data) setInfo(load.data); }, [load.data]);
  useLiveEvent('backup', () => load.reload());
  useLiveEvent('restore', (e) => {
    if (e.state === 'restoring') { setRestoring(e.id); return; }
    setRestoring(null);
    setNote(e.state === 'done' ? 'Restored. My app is running on that data again; the chat has the details.' : null);
    if (e.state === 'failed') setErr('The restore failed; the chat says why.');
    load.reload();
  });

  if (load.error && !info) return <div className="page"><ErrorNote error={load.error} onRetry={load.reload} /></div>;
  if (!info) return <div className="page"><Loading /></div>;

  const take = async () => {
    setTaking(true);
    setErr(null);
    setNote(null);
    try { await api.takeBackup(); setNote('Backed up.'); load.reload(); } catch (e) { setErr(errorMessage(e)); } finally { setTaking(false); }
  };
  const restore = async (b: Backup) => {
    setErr(null);
    setNote(null);
    setRestoring(b.id);
    try { await api.restoreBackup(b.id); } catch (e) { setRestoring(null); throw e; }
  };

  return (
    <div className="page">
      <PageHeader
        title="Backups"
        sub="Copies of my app's data. I take one before every new generation goes live, and once a day."
        actions={<button className="btn btn-sm btn-primary" onClick={take} disabled={taking || !!info.unavailable}>{taking ? 'Backing up…' : 'Back up now'}</button>}
      />
      {info.unavailable && <div className="evo-warn">I can't make backups here: {info.unavailable}.</div>}
      {restoring && <div className="evo-warn" role="status">Restoring… my app is down until it's done.</div>}
      {note && <div className="small saved-note" role="status">{note}</div>}
      {err && <div className="error-text small">{err}</div>}

      <section className="panel">
        <Toggle
          checked={info.daily}
          title="A copy every day"
          on="I copy my app's data once a day and keep the last 7 days."
          off="No daily copies; I still copy the data before every new generation goes live."
          onChange={async (next) => { const d = await api.setDailyBackups(next); setInfo(d); return d.daily; }}
        />
        <p className="small muted backups-keep">
          I keep the last {info.keep.before_generation} copies taken before a new generation, {info.keep.daily} daily ones,
          {' '}{info.keep.manual} you made, and {info.keep.before_restore} taken before a restore. To go back to an older version of me
          together with its data, use <strong>Roll back</strong> in Generations.
        </p>
      </section>

      {info.backups.length === 0 ? (
        <Empty title="No backups yet">The first one is taken before my next generation goes live, or now with Back up now.</Empty>
      ) : (
        <ul className="backup-list panel">
          {info.backups.map((b) => <BackupRow key={b.id} b={b} busy={restoring === b.id} onRestore={restore} />)}
        </ul>
      )}
    </div>
  );
}
