import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, evolutionTitle, errorMessage, isActive, isWaitingOnOwner, shortCommit, usd, type EvolutionEvent } from '../api';
import { useEvolution, useLiveEvent } from '../live';
import { stepProgress } from '../phases';
import { ActivityTicker } from './ActivityTicker';
import { EventList, isToolEvent, mergeEvents } from './EventList';
import { PhaseBar, PhaseList } from './PhaseList';
import { PreviewPanel, awaitingPreview } from './PreviewPanel';
import { Clarifications, QuestionPrompt } from './Questions';
import { Roadmap, StageBadge } from './Roadmap';
import { Sprout } from './Sprout';
import { EvolutionBadge } from './ui';

const OPEN_KEY = 'seed-evo-expanded';

function readExpanded(): string[] {
  try { return JSON.parse(localStorage.getItem(OPEN_KEY) || '[]'); } catch { return []; }
}

/** Whether a card is expanded, remembered per evolution (last 50). */
function useExpanded(id: string): [boolean, (v: boolean) => void] {
  const [expanded, setExpanded] = useState(() => readExpanded().includes(id));
  const set = (v: boolean) => {
    setExpanded(v);
    try {
      const ids = readExpanded().filter((x) => x !== id);
      if (v) ids.push(id);
      localStorage.setItem(OPEN_KEY, JSON.stringify(ids.slice(-50)));
    } catch { /* ignore */ }
  };
  return [expanded, set];
}

export function EvolutionCard({ id }: { id: string }) {
  const evo = useEvolution(id);
  const [expanded, setExpanded] = useExpanded(id);
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
  // Before there's a plan, the intent stands in for a title, without the
  // owner's words the kernel appends for the agent (the chat shows them).
  const title = evolutionTitle(evo);
  const activeStep = prog.active >= 0 && prog.active < steps.length ? steps[prog.active] : null;

  if (!expanded) {
    return (
      <div className={`evo-card evo-compact${active ? ' is-active' : ''}${waiting ? ' is-waiting' : ''} evo-${evo.status}`}>
        <div className="evo-card-head">
          <Sprout evolution={evo} size={28} />
          <div className="evo-card-title">
            <span className="evo-kicker">{isRollback ? 'Rollback' : 'Evolution'}</span>
            <span className="evo-title-row">
              <Link to={`/evolutions/${evo.id}`} className="evo-title evo-title-clamp" title={title}>{title}</Link>
              <StageBadge plan={evo.plan} />
            </span>
          </div>
          <EvolutionBadge status={evo.status} />
        </div>
        {waiting && <QuestionPrompt key={JSON.stringify(evo.questions)} evolution={evo} />}
        <PhaseBar evolution={evo} />
        {steps.length > 0 && evo.status !== 'complete' && (
          <div className="evo-plan-line small">
            <span className="muted">Plan {Math.min(prog.done, steps.length)}/{steps.length}</span>
            {activeStep && <span className="truncate">· {activeStep.title}</span>}
          </div>
        )}
        {awaitingPreview(evo)
          ? <PreviewPanel evolution={evo} />
          : working && <ActivityTicker events={events} status={evo.status} />}
        {evo.status === 'complete' && evo.new_generation != null && (
          <div className="evo-done">
            <span className="leaf" aria-hidden>●</span>
            <span>I am now generation {evo.new_generation}.</span>
            {evo.commit && <code className="commit">{shortCommit(evo.commit)}</code>}
          </div>
        )}
        {evo.status === 'needs_input' && !waiting && <div className="evo-warn">Waiting for your input.</div>}
        {evo.error && <div className="evo-error evo-error-clamp">{evo.error}</div>}
        <button type="button" className="evo-expand" onClick={() => setExpanded(true)} aria-expanded={false}>
          Show details <span aria-hidden>▾</span>
        </button>
      </div>
    );
  }

  return (
    <div className={`evo-card${active ? ' is-active' : ''}${waiting ? ' is-waiting' : ''} evo-${evo.status}`}>
      <div className="evo-card-head">
        <Sprout evolution={evo} size={40} />
        <div className="evo-card-title">
          <span className="evo-kicker">{isRollback ? 'Rollback' : 'Evolution'}</span>
          <span className="evo-title-row">
            <Link to={`/evolutions/${evo.id}`} className="evo-title">{evolutionTitle(evo)}</Link>
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

      {awaitingPreview(evo)
          ? <PreviewPanel evolution={evo} />
          : working && <ActivityTicker events={events} status={evo.status} />}

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

      <button type="button" className="evo-expand" onClick={() => setExpanded(false)} aria-expanded>
        Show less <span aria-hidden>▴</span>
      </button>
      <details className="evo-details" onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
        <summary>Technical details</summary>
        <div className="evo-details-body">
          <div className="kv-inline">
            <span><span className="muted">id</span> <code>{evo.id}</code></span>
            {evo.branch && <span><span className="muted">branch</span> <code>{evo.branch}</code></span>}
            <span><span className="muted">attempts</span> {evo.attempts}</span>
            {evo.usage?.cost_usd ? <span><span className="muted">cost</span> {usd(evo.usage.cost_usd)}</span> : null}
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
