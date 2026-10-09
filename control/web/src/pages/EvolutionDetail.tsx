import { useEffect, useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, evolutionTitle, errorMessage, isActive, isWaitingOnOwner, shortCommit, type Diff, type EvolutionEvent } from '../api';
import { ActivityTicker } from '../components/ActivityTicker';
import { DiffView } from '../components/DiffView';
import { EventList, isToolEvent, mergeEvents } from '../components/EventList';
import { PhaseList } from '../components/PhaseList';
import { Clarifications, QuestionPrompt } from '../components/Questions';
import { Roadmap, StageBadge } from '../components/Roadmap';
import { Sprout } from '../components/Sprout';
import { ErrorNote, EvolutionBadge, Loading, PageHeader, Time, absTime, useLoad } from '../components/ui';
import { useEvolution, useLiveEvent } from '../live';
import { dedupeChecks } from '../phases';

function List({ items }: { items?: string[] }) {
  if (!items?.length) return <span className="muted">—</span>;
  return <ul className="plain-list">{items.map((x, i) => <li key={i}>{x}</li>)}</ul>;
}

export default function EvolutionDetail() {
  const { id = '' } = useParams();
  const load = useLoad(() => api.evolution(id), [id]);
  const evo = useEvolution(id, load.data?.evolution ?? null);
  const [events, setEvents] = useState<EvolutionEvent[]>([]);
  const [showTools, setShowTools] = useState(false);
  const [tab, setTab] = useState<'timeline' | 'diff'>('timeline');
  const [diff, setDiff] = useState<Diff | null>(null);
  const [diffErr, setDiffErr] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);

  useEffect(() => { setEvents([]); setDiff(null); setDiffErr(null); }, [id]);
  useEffect(() => { if (load.data) setEvents((prev) => mergeEvents(prev, load.data!.events)); }, [load.data]);
  useLiveEvent('evolution_event', (e) => { if (e.evolution_id === id) setEvents((prev) => mergeEvents(prev, [e])); });

  const status = evo?.status;
  useEffect(() => {
    if (tab !== 'diff') return;
    let cancelled = false;
    setDiffErr(null);
    api.evolutionDiff(id)
      .then((d) => { if (!cancelled) setDiff(d); })
      .catch((e) => { if (!cancelled) setDiffErr(errorMessage(e)); });
    return () => { cancelled = true; };
  }, [tab, id, status]);

  const visible = useMemo(() => (showTools ? events : events.filter((e) => !isToolEvent(e))), [events, showTools]);
  const toolCount = events.length - events.filter((e) => !isToolEvent(e)).length;

  if (load.error && !evo) return <div className="page"><ErrorNote error={load.error} onRetry={load.reload} /></div>;
  if (!evo) return <div className="page"><Loading /></div>;

  const cancel = async () => {
    setCancelling(true);
    setActionErr(null);
    try { await api.cancelEvolution(evo.id); } catch (e) { setActionErr(errorMessage(e)); } finally { setCancelling(false); }
  };

  const checks = dedupeChecks(evo.checks);
  const r = evo.reflection;

  return (
    <div className="page">
      <div className="crumbs"><Link to="/evolutions">Evolutions</Link> <span className="muted">/</span> <code>{evo.id}</code></div>
      <PageHeader
        title={<>{evo.kind === 'rollback' && <span className="tag">rollback</span>}{evolutionTitle(evo)}<StageBadge plan={evo.plan} /></>}
        sub={
          <span className="meta-row">
            <EvolutionBadge status={evo.status} />
            <span className="mono">gen {evo.base_generation} → {evo.new_generation ?? '…'}</span>
            {evo.commit && <code className="commit">{shortCommit(evo.commit)}</code>}
            {evo.branch && <span className="mono muted">{evo.branch}</span>}
            <span className="muted">attempts {evo.attempts}</span>
            <span className="muted" title={absTime(evo.created_at)}>started <Time iso={evo.created_at} /></span>
            {evo.completed_at && <span className="muted" title={absTime(evo.completed_at)}>finished <Time iso={evo.completed_at} /></span>}
          </span>
        }
        actions={isActive(evo.status) && (
          <button className="btn btn-danger-ghost" onClick={cancel} disabled={cancelling}>{cancelling ? 'Cancelling…' : 'Cancel evolution'}</button>
        )}
      />
      {actionErr && <ErrorNote error={actionErr} />}

      <div className="detail-grid">
        <div className="detail-main">
          {isWaitingOnOwner(evo) && <QuestionPrompt key={JSON.stringify(evo.questions)} evolution={evo} />}

          <section className="panel">
            <h2>Intent</h2>
            <p className="intent-text">{evo.intent || <span className="muted">—</span>}</p>
          </section>

          {!!evo.clarifications?.length && (
            <section className="panel">
              <h2>What you told me</h2>
              <Clarifications items={evo.clarifications} />
            </section>
          )}

          {evo.plan?.stages?.length ? (
            <section className="panel">
              <h2>Roadmap <span className="muted small">this evolution builds stage {evo.plan.stage} of {evo.plan.stages.length}</span></h2>
              <Roadmap plan={evo.plan} full currentDone={evo.status === 'complete'} />
            </section>
          ) : null}

          {evo.error && (
            <section className="panel panel-bad">
              <h2>Error</h2>
              <pre className="code-block">{evo.error}</pre>
            </section>
          )}

          {evo.plan && (
            <section className="panel">
              <h2>Plan <span className="muted small mono">{evo.plan.scope}</span></h2>
              {evo.plan.summary && <p>{evo.plan.summary}</p>}
              <ol className="plan-steps">
                {evo.plan.steps.map((s, i) => (
                  <li key={i}><div className="fg">{s.title}</div>{s.detail && <div className="muted small">{s.detail}</div>}</li>
                ))}
              </ol>
              <div className="two-col">
                <div><h3>Capabilities</h3><List items={evo.plan.capabilities} /></div>
                <div><h3>Risks</h3><List items={evo.plan.risks} /></div>
              </div>
            </section>
          )}

          {evo.summary && (
            <section className="panel">
              <h2>Builder summary</h2>
              <p className="prewrap">{evo.summary}</p>
            </section>
          )}

          <section className="panel">
            <h2>Checks</h2>
            {checks.length === 0 ? <div className="muted">No checks yet.</div> : (
              <div className="table-wrap">
                <table className="table table-compact">
                  <thead><tr><th>Check</th><th>Result</th><th className="num">Attempt</th><th className="num">Passed</th><th className="num">Failed</th><th>Detail</th></tr></thead>
                  <tbody>
                    {checks.map((c) => (
                      <tr key={`${c.name}#${c.attempt}`}>
                        <td className="mono">{c.name}</td>
                        <td><span className={c.ok ? 'ok-text' : 'bad-text'}>{c.ok ? '✓ ok' : '✗ failed'}</span></td>
                        <td className="num mono">{c.attempt}</td>
                        <td className="num mono">{c.tests_passed ?? ''}</td>
                        <td className="num mono">{c.tests_failed ?? ''}</td>
                        <td className="small">{c.detail}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {r && (
            <section className="panel">
              <h2>Reflection <span className={`small ${r.goal_satisfied ? 'ok-text' : 'warn-text'}`}>{r.goal_satisfied ? '✓ goal satisfied' : 'goal not fully satisfied'}</span></h2>
              {r.summary && <p>{r.summary}</p>}
              {!r.goal_satisfied && (r.gaps?.length ?? 0) > 0 && (
                <div className="gaps">
                  <h3>What falls short</h3>
                  <List items={r.gaps ?? []} />
                </div>
              )}
              <div className="two-col">
                <div><h3>Decisions</h3><List items={r.decisions} /></div>
                <div><h3>Learnings</h3><List items={r.learnings} /></div>
                <div><h3>Skills</h3><List items={r.skills} /></div>
                <div><h3>Debt</h3><List items={r.debt} /></div>
              </div>
            </section>
          )}

          <section className="panel">
            <div className="tabs">
              <button className={tab === 'timeline' ? 'active' : ''} onClick={() => setTab('timeline')}>Timeline <span className="count">{events.length}</span></button>
              <button className={tab === 'diff' ? 'active' : ''} onClick={() => setTab('diff')}>Diff</button>
              <span className="spacer" />
              {tab === 'timeline' && (
                <label className="toggle small">
                  <input type="checkbox" checked={showTools} onChange={(e) => setShowTools(e.target.checked)} />
                  Show tool events{toolCount ? ` (${toolCount})` : ''}
                </label>
              )}
            </div>
            {tab === 'timeline' ? <EventList events={visible} /> : diffErr ? <ErrorNote error={diffErr} /> : diff ? <DiffView stat={diff.stat} diff={diff.diff} /> : <Loading label="Loading diff…" />}
          </section>
        </div>

        <aside className="detail-side">
          <section className="panel sticky">
            <div className="detail-sprout"><Sprout evolution={evo} size={104} /></div>
            {isActive(evo.status) && evo.status !== 'needs_input' && <ActivityTicker events={events} status={evo.status} />}
            <h2>Progress</h2>
            <PhaseList evolution={evo} />
          </section>
        </aside>
      </div>
    </div>
  );
}
