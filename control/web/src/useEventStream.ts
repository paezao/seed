import { useEffect, useRef, useState } from 'react';
import { API_BASE, LIVE_EVENT_NAMES, type LiveEvent } from './api';

/**
 * Subscribes to the kernel's SSE stream. The browser's EventSource retries on its
 * own while the connection is merely interrupted; when it gives up (readyState
 * CLOSED, e.g. after a non-200 response) we reconnect ourselves with backoff.
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
    let es: EventSource | null = null;
    let timer: number | undefined;
    let backoff = 1000;
    let disposed = false;

    const connect = () => {
      if (disposed) return;
      es = new EventSource(`${API_BASE}/events`);
      es.onopen = () => {
        backoff = 1000;
        setConnected(true);
        handlers.current.onOpen();
      };
      es.onerror = () => {
        setConnected(false);
        if (es && es.readyState === EventSource.CLOSED) {
          es.close();
          es = null;
          timer = window.setTimeout(connect, backoff);
          backoff = Math.min(backoff * 2, 15000);
        }
      };
      for (const name of LIVE_EVENT_NAMES) {
        es.addEventListener(name, (ev) => {
          let data: unknown;
          try {
            data = JSON.parse((ev as MessageEvent<string>).data);
          } catch {
            return;
          }
          handlers.current.onEvent({ type: name, data } as LiveEvent);
        });
      }
    };

    connect();
    return () => {
      disposed = true;
      window.clearTimeout(timer);
      es?.close();
    };
  }, [reconnectKey]);

  return connected;
}
