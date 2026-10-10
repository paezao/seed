import { useCallback, useEffect, useState } from 'react';
import { api, errorMessage, type NotificationsInfo, type PushDevice } from '../api';
import { Toggle } from './Toggle';
import { Badge, ErrorNote, Loading, relTime, useLoad } from './ui';

// Notifications on this browser: my service worker (/_seed/sw.js) receives
// Web Push messages from the kernel and shows them, even with no tab open.

const supported = () =>
  typeof window !== 'undefined' && window.isSecureContext && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;

const ID_KEY = 'seed-push-device';

function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const pad = '='.repeat((4 - (b64url.length % 4)) % 4);
  const raw = atob((b64url + pad).replace(/-/g, '+').replace(/_/g, '/'));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function registration(): Promise<ServiceWorkerRegistration> {
  return navigator.serviceWorker.register('/_seed/sw.js', { scope: '/_seed/' });
}

async function currentSubscription(): Promise<PushSubscription | null> {
  if (!supported()) return null;
  const reg = await navigator.serviceWorker.getRegistration('/_seed/');
  return reg ? reg.pushManager.getSubscription() : null;
}

/** Sends this browser's subscription to the kernel (idempotent). */
async function register(sub: PushSubscription): Promise<PushDevice> {
  const j = sub.toJSON();
  const d = await api.subscribe({ endpoint: sub.endpoint, p256dh: j.keys?.p256dh ?? '', auth: j.keys?.auth ?? '' });
  try { localStorage.setItem(ID_KEY, d.id); } catch { /* fine */ }
  return d;
}

/** "Chrome on Linux" from a user agent. */
function deviceName(ua: string): string {
  const browser = /Edg\//.test(ua) ? 'Edge' : /OPR\//.test(ua) ? 'Opera' : /Firefox\//.test(ua) ? 'Firefox'
    : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'A browser';
  const os = /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Mac OS X/.test(ua) ? 'macOS'
    : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  return os ? `${browser} on ${os}` : browser;
}

export function NotificationsPanel() {
  const load = useLoad(() => api.notifications(), []);
  const [info, setInfo] = useState<NotificationsInfo | null>(null);
  const [here, setHere] = useState<boolean | null>(null); // this browser is subscribed
  const [hereId, setHereId] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => { if (load.data) setInfo(load.data); }, [load.data]);

  // Is this browser on? If so, make sure I know about it (e.g. after a reset).
  const check = useCallback(async () => {
    const sub = await currentSubscription().catch(() => null);
    if (!sub) { setHere(false); return; }
    try {
      const d = await register(sub);
      setHereId(d.id);
      setHere(true);
      load.reload();
    } catch (e) {
      setHere(false);
      setErr(`This browser allowed notifications, but I couldn't save it: ${errorMessage(e)}`);
    }
  }, []);
  useEffect(() => { void check(); }, [check]);

  const act = async (key: string, fn: () => Promise<void>) => {
    setBusy(key);
    setErr(null);
    setNote(null);
    try { await fn(); } catch (e) { setErr(errorMessage(e)); } finally { setBusy(null); }
  };

  const turnOn = () => act('on', async () => {
    if (!info) return;
    const permission = await Notification.requestPermission();
    if (permission !== 'granted') {
      throw new Error(permission === 'denied'
        ? "Notifications are blocked for this site: allow them in your browser's site settings, then try again."
        : 'Notifications were not allowed.');
    }
    const reg = await registration();
    await navigator.serviceWorker.ready;
    const sub = await reg.pushManager.getSubscription()
      ?? await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(info.public_key) });
    const d = await register(sub);
    setHere(true);
    setHereId(d.id);
    load.reload();
    setNote('Notifications are on for this browser.');
  });

  const turnOff = () => act('off', async () => {
    const sub = await currentSubscription();
    await sub?.unsubscribe();
    let id = hereId;
    try { id = id ?? localStorage.getItem(ID_KEY); localStorage.removeItem(ID_KEY); } catch { /* fine */ }
    if (id) setInfo(await api.unsubscribe(id));
    setHere(false);
    setHereId(null);
  });

  const remove = (d: PushDevice) => act('rm-' + d.id, async () => {
    if (d.id === hereId) { await turnOff(); return; }
    setInfo(await api.unsubscribe(d.id));
  });

  const test = () => act('test', async () => {
    const r = await api.testNotification();
    setNote(`Sent to ${r.delivered} ${r.delivered === 1 ? 'browser' : 'browsers'}.`);
    load.reload();
  });

  return (
    <section className="panel" aria-labelledby="notify-h">
      <h2 id="notify-h">Notifications</h2>
      <p className="small muted">
        I'll tell you when something needs you, even when no tab of mine is open: a change ready to try, a question, a problem I found.
      </p>
      {load.error && !info && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!info && load.loading && <Loading />}
      {info && (
        <>
          <div className="notify-actions">
            {!supported() ? (
              <span className="small muted">This browser can't receive notifications here{window.isSecureContext ? '' : ' (it needs https)'}.</span>
            ) : here ? (
              <>
                <Badge tone="ok">on in this browser</Badge>
                <button type="button" className="btn btn-sm" onClick={test} disabled={busy !== null}>{busy === 'test' ? 'Sending…' : 'Send a test'}</button>
                <button type="button" className="btn btn-sm btn-ghost" onClick={turnOff} disabled={busy !== null}>{busy === 'off' ? 'Turning off…' : 'Turn off here'}</button>
              </>
            ) : (
              <button type="button" className="btn btn-sm btn-primary" onClick={turnOn} disabled={busy !== null || here === null}>
                {busy === 'on' ? 'Turning on…' : 'Turn on in this browser'}
              </button>
            )}
          </div>
          {note && <div className="small saved-note" role="status">{note}</div>}
          {err && <div className="error-text small">{err}</div>}

          {info.subscriptions.length > 0 && (
            <>
              <h3>Browsers</h3>
              <ul className="provider-list">
                {info.subscriptions.map((d) => (
                  <li key={d.id} className="provider-row">
                    <span className="provider-row-name">{deviceName(d.user_agent)}</span>
                    {d.id === hereId && <Badge tone="info">this browser</Badge>}
                    <span className="muted small">
                      added {relTime(d.created_at)}{d.last_sent_at ? ` · last notified ${relTime(d.last_sent_at)}` : ''}
                    </span>
                    {d.last_error && <span className="error-text small" title={d.last_error}>last one failed</span>}
                    <span className="spacer" />
                    <button className="btn btn-sm btn-danger-ghost" onClick={() => remove(d)} disabled={busy !== null}>Remove</button>
                  </li>
                ))}
              </ul>
            </>
          )}

          <h3>Tell me about</h3>
          <div className="notify-kinds">
            {info.about.map((k) => (
              <Toggle
                key={k.key}
                checked={info.kinds[k.key] ?? k.default}
                title={k.label}
                on={k.about}
                off={k.about}
                onChange={async (next) => {
                  const r = await api.setNotifyKinds({ [k.key]: next });
                  setInfo(r);
                  return r.kinds[k.key];
                }}
              />
            ))}
          </div>
        </>
      )}
    </section>
  );
}
