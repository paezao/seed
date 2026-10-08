import { useEffect, useState } from 'react';
import { ApiError, api, errorMessage, type KernelStatus } from '../api';
import { useLiveEvent } from '../live';
import { Markdown } from './Markdown';
import { relTime } from './ui';

/** My kernel's status, kept live. */
export function useKernelStatus() {
  const [status, setStatus] = useState<KernelStatus | null>(null);
  useEffect(() => { api.kernel().then(setStatus).catch(() => {}); }, []);
  useLiveEvent('kernel', setStatus);
  return [status, setStatus] as const;
}

const SEEN = 'seed-kernel-event-seen';

/** Updating my kernel: the button, and what to do when it can't go ahead. */
function UpdateButton({ status, onStarted }: { status: KernelStatus; onStarted?: () => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const go = async (force: boolean) => {
    setBusy(true);
    setErr(null);
    try {
      await api.updateKernel(force);
      onStarted?.();
    } catch (e) {
      setConflict(e instanceof ApiError && e.status === 409);
      setErr(errorMessage(e));
      setBusy(false);
    }
  };
  if (status.applying || (busy && !err)) return <span className="small muted">Updating… I'll restart in a moment.</span>;
  return (
    <span className="kernel-update-actions">
      {!conflict && <button className="btn btn-sm btn-primary" onClick={() => go(false)} disabled={busy}>Update</button>}
      {conflict && (
        <button className="btn btn-sm btn-danger" onClick={() => go(true)} disabled={busy}>Replace my kernel changes and update</button>
      )}
      {err && <span className="error-text small">{err}</span>}
    </span>
  );
}

/** A banner across the control plane when a new kernel is available, and
 *  once after a kernel change (updated, or rolled back). */
export function KernelBanner() {
  const [status] = useKernelStatus();
  const [open, setOpen] = useState(false);
  const [seen, setSeen] = useState<string | null>(() => { try { return localStorage.getItem(SEEN); } catch { return null; } });
  if (!status) return null;
  const ev = status.last_event;
  if (ev && ev.at !== seen && Date.now() - Date.parse(ev.at) < 24 * 3600e3) {
    const dismiss = () => { try { localStorage.setItem(SEEN, ev.at); } catch { /* ignore */ } setSeen(ev.at); };
    return (
      <div className={`banner ${ev.ok ? 'banner-ok' : 'banner-warn'}`} role="status">
        <span className={`dot ${ev.ok ? 'dot-ok' : 'dot-bad'}`} />
        <span>{ev.message}</span>
        <span className="spacer" />
        <button className="btn btn-sm btn-ghost" onClick={dismiss}>Dismiss</button>
      </div>
    );
  }
  if (!status.available || !status.latest) return null;
  const rel = status.latest;
  return (
    <div className="banner banner-info kernel-banner" role="status">
      <div className="kernel-banner-row">
        <span className="dot dot-ok" />
        <span>
          <strong>Kernel {rel.version}</strong> is available
          {status.needs_runtime && <span className="muted"> — it needs a new runtime image</span>}.
        </span>
        <span className="spacer" />
        {rel.notes && <button className="btn btn-sm btn-ghost" onClick={() => setOpen((o) => !o)} aria-expanded={open}>{open ? 'Hide' : "What's new"}</button>}
        {status.needs_runtime
          ? <span className="small muted">Run <code>seed stop && seed upgrade && seed run</code>, or redeploy the new image.</span>
          : <UpdateButton status={status} />}
      </div>
      {open && rel.notes && <div className="kernel-notes"><Markdown source={rel.notes} /></div>}
    </div>
  );
}

/** Settings: my kernel, checking for and installing new ones. */
export function KernelPanel() {
  const [status, setStatus] = useKernelStatus();
  const [checking, setChecking] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const check = async () => {
    setChecking(true);
    setErr(null);
    try { setStatus(await api.checkKernel()); } catch (e) { setErr(errorMessage(e)); } finally { setChecking(false); }
  };
  return (
    <section className="panel" aria-labelledby="kernel-h">
      <h2 id="kernel-h">Kernel</h2>
      <p className="small muted">
        The part of me that hosts and evolves me. New kernels are published as signed releases; installing one is a new
        generation, and if it fails to start I go back to the last kernel that ran well.
      </p>
      {status && (
        <ul className="provider-list">
          <li className="provider-row">
            <span className="provider-row-name">Running</span>
            <code className="fg">{status.version}</code>
          </li>
          <li className="provider-row">
            <span className="provider-row-name">Latest release</span>
            {status.latest ? <code className="fg">{status.latest.version}</code> : <span className="muted small">unknown</span>}
            {status.latest && !status.available && <span className="muted small">you're up to date</span>}
            <span className="spacer" />
            <span className="muted small">{status.checked_at ? `checked ${relTime(status.checked_at)}` : 'not checked yet'}</span>
            <button className="btn btn-sm" onClick={check} disabled={checking}>{checking ? 'Checking…' : 'Check now'}</button>
          </li>
          {status.available && status.latest && (
            <li className="provider-row">
              <span className="provider-row-name">Update</span>
              {status.needs_runtime
                ? <span className="small muted">Needs a new runtime image: run <code>seed stop && seed upgrade && seed run</code>, or redeploy.</span>
                : <UpdateButton status={status} />}
            </li>
          )}
        </ul>
      )}
      {status?.available && status.latest?.notes && <div className="kernel-notes"><Markdown source={status.latest.notes} /></div>}
      {(err || status?.check_error) && <p className="error-text small">{err || `Last check failed: ${status?.check_error}`}</p>}
      {status?.last_event && (
        <details className="kernel-event">
          <summary className="small">{status.last_event.ok ? '✓' : '✗'} {status.last_event.message} <span className="muted">· {relTime(status.last_event.at)}</span></summary>
          {status.last_event.log && <pre className="mono small">{status.last_event.log}</pre>}
        </details>
      )}
    </section>
  );
}
