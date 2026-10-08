import { useEffect, useRef, useState } from 'react';
import { API_BASE, CONTROL_TOKEN, LIVE_EVENT_NAMES, type LiveEvent } from './api';

/**
 * Subscribes to the kernel's SSE stream. It uses fetch() rather than
 * EventSource so the API token travels in a header, never in a URL (URLs end
 * up in proxy logs). Reconnects with backoff; a 401 means this browser was
 * signed out, so the page reloads (and shows the sign-in page).
 *
 * `onOpen` fires on every (re)connect so callers can resync state they may have
 * missed while disconnected.
 */
export function useEventStream(
  onEvent: (e: LiveEvent) => void,
  onOpen: () => void,
  reconnectKey = 0,
): boolean {
  const [connected, setConnected] = useState(false);
  const handlers = useRef({ onEvent, onOpen });
  handlers.current = { onEvent, onOpen };

  useEffect(() => {
    let timer: number | undefined;
    let backoff = 1000;
    let disposed = false;
    const abort = new AbortController();
    const names = new Set<string>(LIVE_EVENT_NAMES);

    const dispatch = (block: string) => {
      let name = 'message';
      const data: string[] = [];
      for (const line of block.split('\n')) {
        if (line.startsWith('event:')) name = line.slice(6).trim();
        else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
      }
      if (!names.has(name) || data.length === 0) return;
      let parsed: unknown;
      try {
        parsed = JSON.parse(data.join('\n'));
      } catch {
        return;
      }
      handlers.current.onEvent({ type: name, data: parsed } as LiveEvent);
    };

    const retry = () => {
      setConnected(false);
      if (disposed) return;
      timer = window.setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 15000);
    };

    const connect = async () => {
      if (disposed) return;
      try {
        const res = await fetch(`${API_BASE}/events`, {
          headers: { 'X-Seed-Token': CONTROL_TOKEN, Accept: 'text/event-stream' },
          signal: abort.signal,
          cache: 'no-store',
        });
        if (res.status === 401) {
          window.location.reload();
          return;
        }
        if (!res.ok || !res.body) return retry();
        backoff = 1000;
        setConnected(true);
        handlers.current.onOpen();
        const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
        let buf = '';
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += value.replace(/\r\n?/g, '\n');
          let i: number;
          while ((i = buf.indexOf('\n\n')) >= 0) {
            dispatch(buf.slice(0, i));
            buf = buf.slice(i + 2);
          }
        }
        retry();
      } catch {
        if (!disposed) retry();
      }
    };

    connect();
    return () => {
      disposed = true;
      window.clearTimeout(timer);
      abort.abort();
    };
  }, [reconnectKey]);

  return connected;
}
