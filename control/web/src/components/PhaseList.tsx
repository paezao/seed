import type { Evolution } from '../api';
import { derivePhases, type PhaseState } from '../phases';

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
            {p.notes.map((n, i) => (
              <span key={i} className={`phase-note ${n.ok ? 'ok' : 'bad'}`} title={n.detail || undefined}>
                {n.ok ? '✓' : '✗'} {n.text}
              </span>
            ))}
          </span>
        </li>
      ))}
    </ol>
  );
}
