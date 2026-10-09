import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { api, errorMessage, type LogSource } from '../api';
import { PageHeader } from '../components/ui';

export default function Logs() {
  const [source, setSource] = useState<LogSource>('organism');
  const [lines, setLines] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [auto, setAuto] = useState(true);
  const box = useRef<HTMLPreElement>(null);
  const stick = useRef(true);
  const seq = useRef(0);

  const fetchLogs = useCallback(() => {
    const n = ++seq.current;
    api.logs(source, 500)
      .then((r) => { if (n === seq.current) { setLines(r.lines ?? []); setError(null); } })
      .catch((e) => { if (n === seq.current) setError(errorMessage(e)); });
  }, [source]);

  useEffect(() => { setLines(null); stick.current = true; fetchLogs(); }, [fetchLogs]);
  useEffect(() => {
    if (!auto) return;
    const t = window.setInterval(fetchLogs, 3000);
    return () => window.clearInterval(t);
  }, [auto, fetchLogs]);

  useLayoutEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const onScroll = () => {
    const el = box.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  return (
    <div className="page page-fill">
      <PageHeader
        title="Logs"
        actions={
          <>
            <button type="button" className={`chip-toggle${auto ? ' is-on' : ''}`} aria-pressed={auto} onClick={() => setAuto((v) => !v)}>
              <span className="chip-toggle-dot" aria-hidden />
              Auto-refresh (3s)
            </button>
            <button className="btn btn-sm" onClick={fetchLogs}>Refresh</button>
          </>
        }
      />
      <div className="tabs">
        {(['organism', 'kernel'] as LogSource[]).map((s) => (
          <button key={s} className={source === s ? 'active' : ''} onClick={() => setSource(s)}>{s}</button>
        ))}
        <span className="spacer" />
        {error && <span className="error-text small">{error}</span>}
        {lines && <span className="muted small mono">{lines.length} lines</span>}
      </div>
      <pre className="logs" ref={box} onScroll={onScroll}>
        {lines === null ? <span className="muted">Loading…</span> : lines.length === 0 ? <span className="muted">No output.</span> : lines.join('\n')}
      </pre>
    </div>
  );
}
