import { useState } from 'react';
import { askMessage, type PointedAsk } from '../pointedAsk';
import { KernelImage } from './KernelImage';

export function PointedAskCard({ ask, onSend, onCancel }: {
  ask: PointedAsk;
  onSend: (content: string, drafts: string[]) => Promise<boolean>;
  onCancel: () => void;
}) {
  const [words, setWords] = useState(ask.words);
  const [busy, setBusy] = useState(false);
  const [shot, setShot] = useState(ask.shot);
  const send = async () => {
    if (!words.trim() || busy) return;
    setBusy(true);
    if (!(await onSend(askMessage(ask, words, !!shot), shot ? [shot] : []))) setBusy(false);
  };
  return (
    <div className="pointed">
      <div className="pointed-head">
        <span className="pointed-label">You selected an area</span>
        <span className="muted small">on <code>{ask.path}</code></span>
      </div>
      {shot
        ? <div className="pointed-shot"><KernelImage path={`/ask-drafts/${shot}`} alt="The area of the page you selected" onMissing={() => setShot(undefined)} /></div>
        : <div className="muted small pointed-noshot">I couldn't take a picture of it; describe it in words.</div>}
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
