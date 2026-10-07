import { useState } from 'react';
import type { Evolution } from '../api';
import { derivePhases, type PhaseNote, type PhaseState } from '../phases';

const ICON: Record<PhaseState, string> = {
  done: '✓', active: '', failed: '✗', waiting: '…', repairing: '↻', pending: '', skipped: '–',
};

export function PhaseList({ evolution, horizontal }: { evolution: Evolution; horizontal?: boolean }) {
  const phases = derivePhases(evolution);
  return (
    <ol className={`phases${horizontal ? ' horizontal' : ''}`}>
      {phases.map((p) => (
        <li key={p.key} className={`phase phase-${p.state}`}>
          <span className="phase-icon" aria-hidden>{p.state === 'active' ? <span className="spinner" /> : ICON[p.state]}</span>
          <span className="phase-body">
            <span className="phase-label">{p.label}</span>
            {p.notes.map((n, i) => <Note key={i} note={n} />)}
          </span>
        </li>
      ))}
    </ol>
  );
}

/** A phase note; when it has an explanation, clicking it shows why. */
function Note({ note: n }: { note: PhaseNote }) {
  const [open, setOpen] = useState(false);
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
