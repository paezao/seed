import { useState } from 'react';
import type { EvolutionEvent } from '../api';
import { JsonBlock, clockTime } from './ui';

const TOOL_KINDS = new Set(['tool_call', 'tool_result']);
export const isToolEvent = (e: EvolutionEvent) => TOOL_KINDS.has(e.kind);

const hasData = (d: unknown) =>
  d !== null && d !== undefined && !(typeof d === 'object' && Object.keys(d as object).length === 0) && d !== '';

function EventRow({ e }: { e: EvolutionEvent }) {
  const [open, setOpen] = useState(false);
  const expandable = hasData(e.data);
  return (
    <li className={`ev ev-${e.kind}${open ? ' open' : ''}`}>
      <button className="ev-row" onClick={() => expandable && setOpen(!open)} disabled={!expandable} aria-expanded={expandable ? open : undefined}>
        <span className="ev-time">{clockTime(e.created_at)}</span>
        <span className="ev-kind">{e.kind.replace('_', ' ')}</span>
        <span className="ev-summary">{e.summary}</span>
        {expandable && <span className="ev-caret">{open ? '−' : '+'}</span>}
      </button>
      {open && <div className="ev-data"><JsonBlock value={e.data} /></div>}
    </li>
  );
}

export function EventList({ events, compact }: { events: EvolutionEvent[]; compact?: boolean }) {
  if (!events.length) return <div className="muted pad-sm">No events.</div>;
  return (
    <ul className={`events${compact ? ' compact' : ''}`}>
      {events.map((e) => <EventRow key={e.id} e={e} />)}
    </ul>
  );
}

export function mergeEvents(prev: EvolutionEvent[], next: EvolutionEvent[]): EvolutionEvent[] {
  const map = new Map<number, EvolutionEvent>();
  for (const e of prev) map.set(e.id, e);
  for (const e of next) map.set(e.id, e);
  return [...map.values()].sort((a, b) => a.id - b.id);
}
