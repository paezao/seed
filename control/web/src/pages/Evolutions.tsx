import { useEffect, useMemo } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, evolutionTitle } from '../api';
import { Toggle } from '../components/Toggle';
import { Empty, ErrorNote, EvolutionBadge, Loading, PageHeader, Time, useLoad } from '../components/ui';
import { useLive } from '../live';

export default function Evolutions() {
  const { evolutions, upsertEvolution } = useLive();
  const load = useLoad(() => api.evolutions(), []);
  const nav = useNavigate();

  useEffect(() => { load.data?.forEach(upsertEvolution); }, [load.data, upsertEvolution]);

  // Prefer live copies; include evolutions that appeared via SSE after load.
  const list = useMemo(() => {
    const ids = new Set<string>((load.data ?? []).map((e) => e.id));
    Object.keys(evolutions).forEach((id) => ids.add(id));
    return [...ids]
      .map((id) => evolutions[id] ?? load.data?.find((e) => e.id === id)!)
      .filter(Boolean)
      .sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
  }, [load.data, evolutions]);

  return (
    <div className="page">
      <PageHeader title="Evolutions" sub="How I have changed myself." />
      <PreviewSwitch />
      {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading ? <Loading /> : list.length === 0 ? (
        <Empty title="No evolutions yet">Tell me what to become in Chat.</Empty>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>Status</th><th>Evolution</th><th>Generation</th><th className="num">Attempts</th><th>Started</th></tr>
            </thead>
            <tbody>
              {list.map((e) => (
                <tr key={e.id} className="clickable" onClick={() => nav(`/evolutions/${e.id}`)}>
                  <td><EvolutionBadge status={e.status} /></td>
                  <td className="cell-main">
                    <Link to={`/evolutions/${e.id}`} onClick={(ev) => ev.stopPropagation()}>
                      {e.kind === 'rollback' && <span className="tag">rollback</span>}
                      <span className="evo-list-title">{evolutionTitle(e)}</span>
                    </Link>
                    {e.intent && evolutionTitle(e) !== e.intent && <div className="muted small truncate">{e.intent.split('\n')[0]}</div>}
                  </td>
                  <td className="mono">
                    {e.base_generation}
                    <span className="muted"> → </span>
                    {e.new_generation ?? <span className="muted">…</span>}
                  </td>
                  <td className="num mono">{e.attempts}</td>
                  <td className="muted nowrap"><Time iso={e.created_at} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function PreviewSwitch() {
  const load = useLoad(() => api.evolutionSettings(), []);
  return (
    <section className="panel preview-switch">
      <Toggle
        checked={load.data?.preview ?? true}
        disabled={!load.data}
        title="Let me try changes before they go live"
        on="When I've built and checked a change, I wait for you to try it on a copy of your data, then apply it, ask me for changes, or discard it. Fixes I start on my own don't wait."
        off="When I've built and checked a change, it goes live right away. You can always roll back."
        onChange={async (next) => { const r = await api.setEvolutionSettings(next); load.setData(r); return r.preview; }}
      />
    </section>
  );
}
