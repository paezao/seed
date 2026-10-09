import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, backupOf, errorMessage, shortCommit, type Backup, type Generation } from '../api';
import { Badge, Empty, ErrorNote, Loading, PageHeader, Time, absTime, useLoad } from '../components/ui';
import { useLiveEvent } from '../live';

function OkBadge({ label, ok }: { label: string; ok?: boolean }) {
  if (ok === undefined) return <Badge>{label} ?</Badge>;
  return <Badge tone={ok ? 'ok' : 'bad'}>{ok ? '✓' : '✗'} {label}</Badge>;
}

function RollbackControl({ gen, backup }: { gen: Generation; backup?: Backup }) {
  const [confirming, setConfirming] = useState(false);
  const [withData, setWithData] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const nav = useNavigate();

  const go = async () => {
    setBusy(true);
    setErr(null);
    try {
      const evo = await api.rollback(gen.number, withData && !!backup);
      nav(`/evolutions/${evo.id}`);
    } catch (e) {
      setErr(errorMessage(e));
      setBusy(false);
    }
  };

  if (!confirming) {
    return <button className="btn btn-sm" onClick={() => setConfirming(true)} title={`Roll back to generation ${gen.number}`}>Roll back</button>;
  }
  return (
    <span className="inline-confirm">
      <span className="small">Roll back to gen {gen.number}?</span>
      {backup && (
        <label className="rollback-data small" title={`The copy saved ${absTime(backup.created_at)}; changes to the data since then are lost (a copy of the data of now is kept in Backups).`}>
          <input type="checkbox" checked={withData} onChange={(e) => setWithData(e.target.checked)} disabled={busy} />
          with its data
        </label>
      )}
      <button className="btn btn-sm btn-danger" onClick={go} disabled={busy}>{busy ? 'Starting…' : 'Confirm'}</button>
      <button className="btn btn-sm btn-ghost" onClick={() => { setConfirming(false); setErr(null); }} disabled={busy}>Cancel</button>
      {err && <span className="error-text small">{err}</span>}
    </span>
  );
}

export default function Generations() {
  const load = useLoad(() => api.generations(), []);
  // Copies of the data, so a rollback can bring back the data of then too.
  const backups = useLoad(() => api.backups().catch(() => null), []);
  useLiveEvent('evolution', (e) => { if (e.status === 'complete') { load.reload(); backups.reload(); } });

  return (
    <div className="page">
      <PageHeader title="Generations" sub="Every version of me is a git commit. The current one is what I am right now, at /." />
      {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading ? <Loading /> : !load.data?.length ? (
        <Empty title="No generations yet" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th className="num">Gen</th><th>Commit</th><th>Title</th><th className="num">Tests</th><th>Checks</th><th>Created</th><th /></tr>
            </thead>
            <tbody>
              {load.data.map((g) => (
                <tr key={g.number} className={g.current ? 'row-current' : ''}>
                  <td className="num mono">
                    {g.number}
                  </td>
                  <td><code className="commit" title={g.commit}>{shortCommit(g.commit)}</code></td>
                  <td className="cell-main">
                    <div className="fg">
                      {g.title || <span className="muted">untitled</span>}
                      {g.current && <Badge tone="ok">current</Badge>}
                    </div>
                    {g.intent && <div className="muted small truncate">{g.intent}</div>}
                    {g.evolution_id && <Link className="small link-quiet" to={`/evolutions/${g.evolution_id}`}>evolution →</Link>}
                  </td>
                  <td className="num mono">{g.tests_passed ?? <span className="muted">—</span>}</td>
                  <td className="nowrap"><span className="badge-row"><OkBadge label="build" ok={g.build_ok} /><OkBadge label="health" ok={g.health_ok} /></span></td>
                  <td className="muted nowrap"><Time iso={g.created_at} /></td>
                  <td className="right nowrap">{!g.current && <RollbackControl gen={g} backup={backups.data ? backupOf(backups.data.backups, g.number) : undefined} />}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
