import { useState } from 'react';
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
 * it will execute; each field is shown verbatim (never reformatted), with
 * invisible and direction-changing characters made visible so nothing can
 * hide in plain sight.
 */
function ApprovalDetail({ detail }: { detail: string }) {
  let fields: [string, string][] | null = null;
  try {
    const parsed = JSON.parse(detail);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      fields = Object.entries(parsed).map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)]);
    }
  } catch { /* not JSON: show as is */ }
  if (!fields) return <pre className="approval-detail">{reveal(detail)}</pre>;
  return (
    <dl className="approval-fields">
      {fields.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd><pre className="approval-detail">{reveal(v)}</pre></dd>
        </div>
      ))}
    </dl>
  );
}

// Bidi controls, zero-width characters, other format/control characters
// (except newline and tab) and the BOM are shown as ⟨U+XXXX⟩.
const HIDDEN = /[\u0000-\u0008\u000B-\u001F\u007F-\u009F\u00AD\u061C\u180E\u200B-\u200F\u202A-\u202E\u2060-\u2064\u2066-\u206F\uFEFF]/g;

function reveal(s: string): string {
  return s.replace(HIDDEN, (c) => `⟨U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}⟩`);
}
