import { useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, type Approval } from '../api';
import { useLive } from '../live';
import { Badge, Time } from './ui';

export function ApprovalCard({ approval }: { approval: Approval }) {
  const { removeApproval } = useLive();
  const [busy, setBusy] = useState<'approve' | 'deny' | null>(null);
  const [error, setError] = useState<string | null>(null);

  const decide = async (approved: boolean) => {
    setBusy(approved ? 'approve' : 'deny');
    setError(null);
    try {
      await api.decide(approval.id, approved);
      removeApproval(approval.id);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const tone = approval.level === 'dangerous' ? 'bad' : approval.level === 'review' ? 'warn' : 'neutral';
  return (
    <div className={`approval approval-${approval.level}`}>
      <div className="approval-head">
        <span className="approval-label">Approval needed</span>
        <Badge tone={tone}>{approval.level}</Badge>
        <span className="spacer" />
        <span className="muted small"><Time iso={approval.created_at} /></span>
      </div>
      <code className="approval-action">{approval.action}</code>
      {approval.detail && <ApprovalDetail detail={approval.detail} />}
      {error && <div className="error-text small">{error}</div>}
      <div className="approval-actions">
        <button className="btn btn-primary" disabled={!!busy} onClick={() => decide(true)}>
          {busy === 'approve' ? 'Approving…' : 'Approve'}
        </button>
        <button className="btn btn-danger-ghost" disabled={!!busy} onClick={() => decide(false)}>
          {busy === 'deny' ? 'Denying…' : 'Deny'}
        </button>
        {approval.evolution_id && (
          <Link className="small muted link-quiet" to={`/evolutions/${approval.evolution_id}`}>View evolution →</Link>
        )}
      </div>
    </div>
  );
}

/**
 * The exact request being approved. The kernel sends the canonical arguments
 * it will execute; each field is shown verbatim (never reformatted). Nothing
 * may hide in it: invisible characters are replaced by visible ⟨U+XXXX⟩
 * markers, and every other non-ASCII character (possible look-alikes, e.g. a
 * Cyrillic "о" in DROP) is highlighted with its code point.
 */
function ApprovalDetail({ detail }: { detail: string }) {
  let fields: [string, string][] | null = null;
  try {
    const parsed = JSON.parse(detail);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      fields = Object.entries(parsed).map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)]);
    }
  } catch { /* not JSON: show as is */ }
  const all = fields ? fields : [['request', detail] as [string, string]];
  const suspicious = all.some(([k, v]) => hasNonASCII(k) || hasNonASCII(v));
  return (
    <>
      {suspicious && (
        <div className="approval-warning">
          This request contains non-ASCII characters (highlighted below). Check them carefully: some look
          like ordinary letters or are invisible.
        </div>
      )}
      <dl className="approval-fields">
        {all.map(([k, v]) => (
          <div key={k}>
            <dt><Revealed text={k} /></dt>
            <dd><pre className="approval-detail"><Revealed text={v} /></pre></dd>
          </div>
        ))}
      </dl>
    </>
  );
}

function hasNonASCII(s: string): boolean {
  return /[^\x09\x0A\x20-\x7E]/u.test(s);
}

// Invisible: control (except tab/newline), format (incl. bidi controls, zero
// widths, tag characters), private use, surrogates, line/paragraph
// separators, non-standard spaces, variation selectors, and other code points
// that render as nothing.
const INVISIBLE = /[\p{Cc}\p{Cf}\p{Co}\p{Cs}\p{Zl}\p{Zp}\p{Zs}\u{034F}\u{115F}\u{1160}\u{17B4}\u{17B5}\u{180B}-\u{180F}\u{3164}\u{FE00}-\u{FE0F}\u{FFA0}\u{E0100}-\u{E01EF}]/u;

function codePoint(c: string): string {
  return 'U+' + c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0');
}

function Revealed({ text }: { text: string }) {
  const parts: ReactNode[] = [];
  let plain = '';
  let i = 0;
  for (const c of text) { // iterates code points (keeps astral characters whole)
    const ascii = c === '\n' || c === '\t' || (c >= ' ' && c <= '~');
    if (ascii) { plain += c; continue; }
    if (plain) { parts.push(plain); plain = ''; }
    const cp = codePoint(c);
    parts.push(INVISIBLE.test(c)
      ? <mark key={i++} className="approval-invisible" title={`invisible character ${cp}`}>⟨{cp}⟩</mark>
      : <mark key={i++} className="approval-nonascii" title={cp}>{c}<sub>{cp}</sub></mark>);
  }
  if (plain) parts.push(plain);
  return <>{parts}</>;
}
