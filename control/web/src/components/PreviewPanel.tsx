import { useState } from 'react';
import { api, errorMessage, type Evolution } from '../api';

/** True when an evolution is waiting for its owner to try it. */
export function awaitingPreview(e: Evolution): boolean {
  return e.status === 'ready' && !!e.preview && (e.preview.state === 'starting' || e.preview.state === 'ready' || e.preview.state === 'failed');
}

/** Try a new generation before it goes live, then apply it, ask for changes, or discard it. */
export function PreviewPanel({ evolution: e }: { evolution: Evolution }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [asking, setAsking] = useState(false);
  const [feedback, setFeedback] = useState('');
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const p = e.preview ?? {};
  const decide = async (action: 'apply' | 'changes' | 'discard') => {
    setBusy(action);
    setErr(null);
    try {
      await api.decideEvolution(e.id, action, action === 'changes' ? feedback : undefined);
    } catch (x) {
      setErr(errorMessage(x));
      setBusy(null);
    }
  };
  if (busy && !err) {
    const what = { apply: 'Going live…', changes: 'Back to work on your changes…', discard: 'Discarding…' }[busy as 'apply' | 'changes' | 'discard'];
    return <div className="preview-panel"><span className="spinner" /> <span className="small">{what}</span></div>;
  }
  return (
    <div className="preview-panel">
      <div className="preview-row">
        <div className="preview-what">
          <strong>{p.state === 'starting' ? 'Getting it ready to try…' : 'Ready: try it before it goes live'}</strong>
          {(p.round ?? 0) > 0 && <span className="muted small"> · round {(p.round ?? 0) + 1}</span>}
        </div>
        {!asking && (
          <div className="preview-actions">
            {p.state === 'ready' && <a className="btn btn-sm btn-primary" href={`/_seed/preview/${encodeURIComponent(e.id)}`}>Try it</a>}
            <button className="btn btn-sm" disabled={p.state === 'starting'} onClick={() => decide('apply')}>Apply</button>
            <button className="btn btn-sm" disabled={p.state === 'starting'} onClick={() => setAsking(true)}>Ask for changes</button>
            {!confirmDiscard
              ? <button className="btn btn-sm btn-ghost" disabled={p.state === 'starting'} onClick={() => setConfirmDiscard(true)}>Discard</button>
              : (
                <span className="inline-confirm">
                  <button className="btn btn-sm btn-danger" onClick={() => decide('discard')}>Discard it</button>
                  <button className="btn btn-sm btn-ghost" onClick={() => setConfirmDiscard(false)}>Keep</button>
                </span>
              )}
          </div>
        )}
      </div>
      {p.state === 'ready' && !asking && (
        <p className="preview-note small muted">
          On {p.data === 'copy' ? 'a copy of your data' : 'test data'}; changes you make while trying it are thrown away, and visitors keep seeing the live version.
        </p>
      )}
      {p.state === 'failed' && <p className="preview-note small warn-text">{p.note || "I couldn't start the preview."} You can still apply or discard it.</p>}
      {asking && (
        <form className="preview-ask" onSubmit={(ev) => { ev.preventDefault(); if (feedback.trim()) void decide('changes'); }}>
          <textarea className="input textarea" rows={3} autoFocus value={feedback} onChange={(x) => setFeedback(x.target.value)}
            placeholder="What should be different? e.g. make the title bigger, and keep the old colors" />
          <div className="preview-actions">
            <button className="btn btn-sm btn-primary" type="submit" disabled={!feedback.trim()}>Send</button>
            <button className="btn btn-sm btn-ghost" type="button" onClick={() => setAsking(false)}>Cancel</button>
          </div>
        </form>
      )}
      {err && <p className="error-text small">{err}</p>}
    </div>
  );
}
