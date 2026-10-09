import { useEffect, useRef, useState } from 'react';
import { usd, type Evolution } from '../api';
import { derivePhases, type PhaseNote, type PhaseState, type PhaseView } from '../phases';

const ICON: Record<PhaseState, string> = {
  done: '✓', active: '', failed: '✗', waiting: '…', repairing: '↻', pending: '', skipped: '–',
};

/** Phases whose notes start open: where something needs looking at. */
const OPEN_BY_DEFAULT: PhaseState[] = ['failed', 'repairing', 'waiting'];

export function PhaseList({ evolution, horizontal }: { evolution: Evolution; horizontal?: boolean }) {
  const phases = derivePhases(evolution);
  return (
    <ol className={`phases${horizontal ? ' horizontal' : ''}`}>
      {phases.map((p) => <PhaseRow key={p.key} phase={p} />)}
    </ol>
  );
}

/** One phase. Its checks fold away behind a summary ("8 checks ✓") unless
 *  something failed there; click to see them. */
function PhaseRow({ phase: p }: { phase: PhaseView }) {
  const anyBad = p.notes.some((n) => !n.ok && !n.wait);
  const [open, setOpen] = useState(OPEN_BY_DEFAULT.includes(p.state) || anyBad);
  const foldable = p.notes.length > 1;
  const good = p.notes.filter((n) => n.ok && !n.wait).length;
  const bad = p.notes.filter((n) => !n.ok && !n.wait).length;
  const summary = [good ? `${good} ✓` : '', bad ? `${bad} ✗` : ''].filter(Boolean).join(' · ');
  return (
    <li className={`phase phase-${p.state}`}>
      <span className="phase-icon" aria-hidden>{p.state === 'active' ? <span className="spinner" /> : ICON[p.state]}</span>
      <span className="phase-body">
        {foldable ? (
          <button type="button" className="phase-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            <span className="phase-label">{p.label}</span>
            <span className={`phase-summary${bad ? ' bad' : ''}`}>{summary}</span>
            <span className="phase-caret" aria-hidden>{open ? '▾' : '▸'}</span>
          </button>
        ) : <span className="phase-label">{p.label}</span>}
        {(open || !foldable) && p.notes.map((n, i) => <Note key={i} note={n} />)}
      </span>
    </li>
  );
}

/** The phases as a slim bar, for the compact card. */
export function PhaseBar({ evolution }: { evolution: Evolution }) {
  const phases = derivePhases(evolution);
  const current = phases.find((p) => ['failed', 'repairing', 'waiting', 'active'].includes(p.state))
    ?? [...phases].reverse().find((p) => p.state === 'done');
  const idx = current ? phases.indexOf(current) + 1 : 0;
  const label = !current ? 'Starting'
    : current.state === 'failed' ? `${current.label} failed`
    : current.state === 'repairing' ? `Repairing ${current.label.toLowerCase()}`
    : current.state === 'waiting' ? `${current.label} · waiting for you`
    : evolution.status === 'complete' ? 'Done'
    : `${current.label} · ${idx} of ${phases.length}`;
  return (
    <div className="phase-bar-wrap">
      <ol className="phase-bar" aria-label={`Progress: ${label}`}>
        {phases.map((p) => <li key={p.key} className={`seg seg-${p.state}`} title={`${p.label}: ${p.state}`} />)}
      </ol>
      <span className="phase-bar-label">{label}</span>
      <EvolutionCost evolution={evolution} />
    </div>
  );
}

/** What an evolution has cost so far; it ticks up live while it works. */
export function EvolutionCost({ evolution, inline }: { evolution: Evolution; inline?: boolean }) {
  const cost = evolution.usage?.cost_usd ?? 0;
  const [bump, setBump] = useState(0);
  const prev = useRef(cost);
  useEffect(() => {
    if (cost > prev.current) setBump((b) => b + 1);
    prev.current = cost;
  }, [cost]);
  if (cost <= 0) return null;
  const calls = evolution.usage?.model_calls ?? 0;
  return (
    <span key={bump} className={`phase-bar-cost${inline ? ' is-inline' : ''}${bump ? ' is-bumped' : ''}`}
      title={`What this evolution has cost so far: ${calls} model ${calls === 1 ? 'call' : 'calls'}`}>
      {usd(cost)}
    </span>
  );
}

/** A phase note; when it has an explanation, clicking it shows why. */
function Note({ note: n }: { note: PhaseNote }) {
  const [open, setOpen] = useState(false);
  if (n.wait) return <span className="phase-note wait">{n.text}</span>;
  const mark = n.ok ? '✓' : '✗';
  if (!n.explain?.length) {
    return <span className={`phase-note ${n.ok ? 'ok' : 'bad'}`} title={n.detail || undefined}>{mark} {n.text}</span>;
  }
  return (
    <span className={`phase-note ${n.ok ? 'ok' : 'bad'}`}>
      <button type="button" className="phase-note-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {mark} {n.text} <span className="phase-note-why">{open ? 'hide' : 'why?'}</span>
      </button>
      {open && (
        <ul className="phase-explain">
          {n.explain.map((x, i) => <li key={i}>{x}</li>)}
        </ul>
      )}
    </span>
  );
}
