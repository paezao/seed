import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { errorMessage, type EvolutionStatus, type OrganismState } from '../api';
import { useLive } from '../live';

export function Badge({ tone = 'neutral', children, pulse }: { tone?: Tone; children: ReactNode; pulse?: boolean }) {
  return (
    <span className={`badge badge-${tone}`}>
      {pulse && <span className="pulse-dot" />}
      {children}
    </span>
  );
}

export type Tone = 'neutral' | 'ok' | 'warn' | 'bad' | 'info' | 'active';

export function evolutionTone(s: EvolutionStatus): Tone {
  switch (s) {
    case 'complete': return 'ok';
    case 'failed': return 'bad';
    case 'needs_input': return 'warn';
    case 'cancelled': case 'rolled_back': return 'neutral';
    case 'ready': return 'info';
    default: return 'active';
  }
}

export function EvolutionBadge({ status }: { status: EvolutionStatus }) {
  const tone = evolutionTone(status);
  return <Badge tone={tone} pulse={tone === 'active'}>{status.replace('_', ' ')}</Badge>;
}

export function organismTone(s: OrganismState): Tone {
  switch (s) {
    case 'running': return 'ok';
    case 'failed': return 'bad';
    case 'stopped': return 'neutral';
    default: return 'active';
  }
}

export function OrganismBadge({ state }: { state: OrganismState }) {
  const tone = organismTone(state);
  return <Badge tone={tone} pulse={tone === 'active'}>{state}</Badge>;
}

export function relTime(iso?: string): string {
  if (!iso) return '';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  if (s < 86400 * 7) return `${Math.round(s / 86400)}d ago`;
  return new Date(iso).toLocaleDateString();
}

export function absTime(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function Time({ iso }: { iso?: string }) {
  return <time dateTime={iso} title={absTime(iso)}>{relTime(iso)}</time>;
}

export function clockTime(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}

/** Load data, reload whenever deps change or the live stream reconnects. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[]) {
  const { resync } = useLive();
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const seq = useRef(0);

  const reload = useCallback(() => {
    const n = ++seq.current;
    setLoading(true);
    fnRef.current()
      .then((d) => { if (n === seq.current) { setData(d); setError(null); } })
      .catch((e) => { if (n === seq.current) setError(errorMessage(e)); })
      .finally(() => { if (n === seq.current) setLoading(false); });
  }, []);

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(reload, [...deps, resync]);
  return { data, setData, error, loading, reload };
}

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return <div className="muted pad">{label}</div>;
}

export function ErrorNote({ error, onRetry }: { error: string; onRetry?: () => void }) {
  return (
    <div className="error-note">
      <span>{error}</span>
      {onRetry && <button className="btn btn-sm" onClick={onRetry}>Retry</button>}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {children && <div className="muted">{children}</div>}
    </div>
  );
}

export function PageHeader({ title, sub, actions }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-header">
      <div>
        <h1>{title}</h1>
        {sub && <div className="page-sub">{sub}</div>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </div>
  );
}

export function JsonBlock({ value }: { value: unknown }) {
  let text: string;
  if (typeof value === 'string') text = value;
  else {
    try { text = JSON.stringify(value, null, 2); } catch { text = String(value); }
  }
  return <pre className="code-block">{text}</pre>;
}
