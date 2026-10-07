import { useEffect, useState } from 'react';
import type { EvolutionEvent, EvolutionStatus } from '../api';

const PREFERRED = new Set(['tool_call', 'check', 'phase', 'question']);

/** The most telling recent event: the latest of a preferred kind, else the latest that isn't a raw tool result. */
export function latestActivity(events: EvolutionEvent[]): EvolutionEvent | null {
  let fallback: EvolutionEvent | null = null;
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (!e.summary) continue;
    if (PREFERRED.has(e.kind)) return e;
    if (!fallback && e.kind !== 'tool_result') fallback = e;
  }
  return fallback;
}

// What the Seed says it is doing before any event has arrived.
const IDLE_TEXT: Partial<Record<EvolutionStatus, string>> = {
  requested: 'getting ready',
  planning: 'thinking about a plan',
  planned: 'about to start',
  mutating: 'rewriting my code',
  building: 'building myself',
  testing: 'running my tests',
  running: 'launching the new me',
  observing: 'checking that I am healthy',
  reflecting: 'reflecting on what I did',
  ready: 'getting ready to become the new generation',
  applying: 'becoming the new generation',
};

type Line = { key: string; text: string };

/**
 * One line of "what I'm doing right now", cross-fading when it changes.
 * Fixed height; render it only while the evolution is working.
 */
export function ActivityTicker({ events, status }: { events: EvolutionEvent[]; status: EvolutionStatus }) {
  const ev = latestActivity(events);
  const current: Line = ev ? { key: `e${ev.id}`, text: ev.summary } : { key: `s${status}`, text: IDLE_TEXT[status] ?? 'working' };
  const [lines, setLines] = useState<Line[]>([current]);

  useEffect(() => {
    setLines((prev) => (prev[prev.length - 1]?.key === current.key ? prev : [...prev.slice(-1), current]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current.key, current.text]);

  return (
    <div className="ticker" title={current.text}>
      <span className="ticker-dots" aria-hidden><i /><i /><i /></span>
      <span className="ticker-track">
        {lines.map((l, i) => (
          <span key={l.key} className={`ticker-line ${i === lines.length - 1 ? 'in' : 'out'}`} aria-hidden={i !== lines.length - 1}>
            {l.text}
          </span>
        ))}
      </span>
    </div>
  );
}
