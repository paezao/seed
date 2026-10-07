import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, isActive, isWaitingOnOwner, shortCommit, type EvolutionEvent } from '../api';
import { useEvolution, useLiveEvent } from '../live';
import { stepProgress } from '../phases';
import { ActivityTicker } from './ActivityTicker';
import { EventList, isToolEvent, mergeEvents } from './EventList';
import { PhaseList } from './PhaseList';
import { Clarifications, QuestionPrompt } from './Questions';
import { Roadmap, StageBadge } from './Roadmap';
import { Sprout } from './Sprout';
import { EvolutionBadge } from './ui';

export function EvolutionCard({ id }: { id: string }) {
  const evo = useEvolution(id);
  const [events, setEvents] = useState<EvolutionEvent[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [open, setOpen] = useState(false);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const active = evo ? isActive(evo.status) : false;

  useLiveEvent('evolution_event', (e) => {
    if (e.evolution_id !== id) return;
    setEvents((prev) => mergeEvents(prev, [e]));
  });

  // Load the timeline when the details open, or right away while the Seed is
  // working so the activity line and plan checklist start from the truth.
  useEffect(() => {
    if ((!open && !active) || loaded) return;
    let cancelled = false;
    api.evolution(id)
      .then((r) => { if (!cancelled) { setEvents((prev) => mergeEvents(prev, r.events)); setLoaded(true); } })
      .catch((e) => { if (!cancelled) setLoadErr(errorMessage(e)); });
    return () => { cancelled = true; };
  }, [open, active, loaded, id]);

  if (!evo) {
    return <div className="evo-card evo-card-loading"><span className="spinner" /> <span className="muted">Loading evolution…</span></div>;
  }

  const waiting = isWaitingOnOwner(evo);
  const working = active && evo.status !== 'needs_input';
  const steps = evo.plan?.steps ?? [];
  const prog = stepProgress(evo, events);
  const isRollback = evo.kind === 'rollback';

  return (
    <div className={`evo-card${active ? ' is-active' : ''}${waiting ? ' is-waiting' : ''} evo-${evo.status}`}>
      <div className="evo-card-head">
        <Sprout evolution={evo} size={40} />
        <div className="evo-card-title">
          <span className="evo-kicker">{isRollback ? 'Rollback' : 'Evolution'}</span>
          <span className="evo-title-row">
            <Link to={`/evolutions/${evo.id}`} className="evo-title">{evo.plan?.title || evo.title || evo.intent}</Link>
            <StageBadge plan={evo.plan} />
          </span>
        </div>
        <EvolutionBadge status={evo.status} />
      </div>
      {evo.intent && (evo.plan?.title || evo.title) && evo.intent !== (evo.plan?.title || evo.title) && (
        <div className="evo-intent">“{evo.intent}”</div>
      )}

      {evo.plan?.summary && <p className="evo-summary">{evo.plan.summary}</p>}

      {waiting && <QuestionPrompt key={JSON.stringify(evo.questions)} evolution={evo} />}

      <Roadmap plan={evo.plan} currentDone={evo.status === 'complete'} />

      <div className="evo-grid">
        {steps.length > 0 && (
          <div className="evo-col">
            <div className="col-label">Plan</div>
            <ol className="steps">
              {steps.map((s, i) => {
                const st = i < prog.done ? 'done' : i === prog.active ? 'active' : 'todo';
                return (
                  <li key={i} className={`step step-${st}`} title={s.detail || undefined}>
                    <span className="step-icon">{st === 'done' ? '✓' : st === 'active' ? <span className="spinner" /> : ''}</span>
                    <span>{s.title}</span>
                  </li>
                );
              })}
            </ol>
          </div>
        )}
        <div className="evo-col">
          <div className="col-label">Progress</div>
          <PhaseList evolution={evo} />
        </div>
      </div>

      {working && <ActivityTicker events={events} status={evo.status} />}

      {evo.status === 'complete' && evo.new_generation != null && (
        <div className="evo-done">
          <span className="leaf" aria-hidden>●</span>
          <span>I am now generation {evo.new_generation}.</span>
          {evo.commit && <code className="commit">{shortCommit(evo.commit)}</code>}
        </div>
      )}
      {evo.status === 'needs_input' && !waiting && <div className="evo-warn">Waiting for your input.</div>}
      {!!evo.clarifications?.length && <div className="evo-clar"><Clarifications items={evo.clarifications} collapsed /></div>}
      {evo.error && <div className="evo-error">{evo.error}</div>}

      <details className="evo-details" onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
        <summary>Technical details</summary>
        <div className="evo-details-body">
          <div className="kv-inline">
            <span><span className="muted">id</span> <code>{evo.id}</code></span>
            {evo.branch && <span><span className="muted">branch</span> <code>{evo.branch}</code></span>}
            <span><span className="muted">attempts</span> {evo.attempts}</span>
            <span><span className="muted">gen</span> {evo.base_generation}{evo.new_generation != null ? ` → ${evo.new_generation}` : ''}</span>
          </div>
          {loadErr && <div className="error-text small">{loadErr}</div>}
          {open && !loaded && !loadErr && <div className="muted small pad-sm">Loading events…</div>}
          {loaded && <EventList events={events} compact />}
          {loaded && events.some(isToolEvent) && (
            <Link className="small link-quiet" to={`/evolutions/${evo.id}`}>Open full timeline →</Link>
          )}
        </div>
      </details>
    </div>
  );
}
