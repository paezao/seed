import { useState } from 'react';
import { askMessage, type PointedAsk } from '../pointedAsk';

export function PointedAskCard({ ask, onSend, onCancel }: {
  ask: PointedAsk;
  onSend: (content: string) => Promise<boolean>;
  onCancel: () => void;
}) {
  const [words, setWords] = useState(ask.words);
  const [busy, setBusy] = useState(false);
  const e = ask.element;
  const send = async () => {
    if (!words.trim() || busy) return;
    setBusy(true);
    if (!(await onSend(askMessage(ask, words)))) setBusy(false);
  };
  return (
    <div className="pointed">
      <div className="pointed-head">
        <span className="pointed-label">You pointed at</span>
        <code className="pointed-what" title={e.selector}>{e.label || e.tag}</code>
        <span className="muted small">on {ask.page.path || '/'}</span>
      </div>
      <textarea
        className="pointed-words"
        value={words}
        maxLength={2000}
        rows={3}
        autoFocus
        aria-label="What should change"
        onChange={(ev) => setWords(ev.target.value)}
        onKeyDown={(ev) => { if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey)) { ev.preventDefault(); void send(); } }}
      />
      <div className="pointed-foot">
        <span className="muted small">Nothing is sent until you press Send.</span>
        <span className="pointed-actions">
          <button type="button" className="btn btn-sm btn-ghost" onClick={onCancel} disabled={busy}>Cancel</button>
          <button type="button" className="btn btn-sm btn-primary" onClick={send} disabled={busy || !words.trim()}>
            {busy ? 'Sending…' : 'Send'}
          </button>
        </span>
      </div>
    </div>
  );
}
