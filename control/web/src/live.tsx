import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  api, onReachability,
  type Approval, type Evolution, type Extension, type LiveEvent, type LiveEventMap, type LiveEventName, type Status,
} from './api';
import { useEventStream } from './useEventStream';

type Live = {
  status: Status | null;
  reachable: boolean;
  streaming: boolean;
  thinking: boolean;
  approvals: Approval[];
  extensions: Extension[];
  evolutions: Record<string, Evolution>;
  /** Increments every time the event stream (re)connects; views refetch on change. */
  resync: number;
  upsertEvolution: (e: Evolution) => void;
  removeApproval: (id: string) => void;
  subscribe: (fn: (e: LiveEvent) => void) => () => void;
  retry: () => void;
  /** Re-fetch status now (e.g. after changing the model). */
  refreshStatus: () => Promise<void>;
};

const LiveContext = createContext<Live | null>(null);

export function useLive(): Live {
  const ctx = useContext(LiveContext);
  if (!ctx) throw new Error('useLive outside LiveProvider');
  return ctx;
}

/** Run `fn` for every live event of the given type. */
export function useLiveEvent<K extends LiveEventName>(type: K, fn: (data: LiveEventMap[K]) => void) {
  const { subscribe } = useLive();
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(
    () => subscribe((e) => { if (e.type === type) ref.current(e.data as LiveEventMap[K]); }),
    [subscribe, type],
  );
}

/** Returns the freshest copy of an evolution, preferring the live store. */
export function useEvolution(id: string | undefined, fallback?: Evolution | null): Evolution | null {
  const { evolutions, upsertEvolution } = useLive();
  const known = id ? evolutions[id] : undefined;
  useEffect(() => {
    if (!id || known) return;
    if (fallback) { upsertEvolution(fallback); return; }
    let cancelled = false;
    api.evolution(id).then((r) => { if (!cancelled) upsertEvolution(r.evolution); }).catch(() => {});
    return () => { cancelled = true; };
  }, [id, known, fallback, upsertEvolution]);
  return known ?? fallback ?? null;
}

const newer = (a: Evolution, b: Evolution | undefined) => !b || a.updated_at >= b.updated_at;

export function LiveProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [reachable, setReachable] = useState(true);
  const [thinking, setThinking] = useState(false);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [extensions, setExtensions] = useState<Extension[]>([]);
  const [evolutions, setEvolutions] = useState<Record<string, Evolution>>({});
  const [resync, setResync] = useState(0);
  const [reconnectKey, setReconnectKey] = useState(0);
  const listeners = useRef(new Set<(e: LiveEvent) => void>());

  const upsertEvolution = useCallback((e: Evolution) => {
    setEvolutions((prev) => (newer(e, prev[e.id]) ? { ...prev, [e.id]: e } : prev));
  }, []);

  const removeApproval = useCallback((id: string) => {
    setApprovals((prev) => prev.filter((a) => a.id !== id));
  }, []);

  const applyStatus = useCallback((s: Status) => {
    setStatus(s);
    if (s.active_evolution) upsertEvolution(s.active_evolution);
  }, [upsertEvolution]);

  const refresh = useCallback(async () => {
    try {
      const s = await api.status();
      applyStatus(s);
      const [ap, ex] = await Promise.all([
        api.approvals().catch(() => null),
        api.extensions().catch(() => null),
      ]);
      if (ap) setApprovals(ap.filter((a) => a.status === 'pending'));
      if (ex) setExtensions(ex);
    } catch {
      /* reachability is reported by the client */
    }
  }, [applyStatus]);

  useEffect(() => onReachability(setReachable), []);
  useEffect(() => { refresh(); }, [refresh]);

  // A new generation may contribute (or remove) admin screens.
  const generation = status?.generation?.number;
  useEffect(() => {
    if (generation == null) return;
    api.extensions().then(setExtensions).catch(() => {});
  }, [generation]);

  // While unreachable, poll until the kernel comes back, then restart the stream.
  useEffect(() => {
    if (reachable) return;
    const t = window.setInterval(refresh, 3000);
    return () => window.clearInterval(t);
  }, [reachable, refresh]);
  const wasReachable = useRef(true);
  useEffect(() => {
    if (reachable && !wasReachable.current) setReconnectKey((k) => k + 1);
    wasReachable.current = reachable;
  }, [reachable]);

  const refreshStatus = useCallback(async () => {
    try { applyStatus(await api.status()); } catch { /* reported by the client */ }
  }, [applyStatus]);

  const onEvent = useCallback((e: LiveEvent) => {
    switch (e.type) {
      case 'status': applyStatus(e.data); break;
      case 'evolution': upsertEvolution(e.data); break;
      case 'chat': setThinking(!!e.data.thinking); break;
      case 'message': if (e.data.role !== 'user') setThinking(false); break;
      // An incident changed: the count in the sidebar comes from the status.
      case 'incident': void refreshStatus(); break;
      case 'approval': {
        const a = e.data;
        setApprovals((prev) => {
          const rest = prev.filter((x) => x.id !== a.id);
          return a.status === 'pending' ? [...rest, a] : rest;
        });
        break;
      }
    }
    listeners.current.forEach((fn) => fn(e));
  }, [applyStatus, upsertEvolution, refreshStatus]);

  const onOpen = useCallback(() => {
    setReachable(true);
    setResync((n) => n + 1);
    refresh();
  }, [refresh]);

  const streaming = useEventStream(onEvent, onOpen, reconnectKey);

  const subscribe = useCallback((fn: (e: LiveEvent) => void) => {
    listeners.current.add(fn);
    return () => { listeners.current.delete(fn); };
  }, []);

  const retry = useCallback(() => {
    refresh();
    setReconnectKey((k) => k + 1);
  }, [refresh]);


  const value = useMemo<Live>(() => ({
    status, reachable, streaming, thinking, approvals, extensions, evolutions, resync,
    upsertEvolution, removeApproval, subscribe, retry, refreshStatus,
  }), [status, reachable, streaming, thinking, approvals, extensions, evolutions, resync, upsertEvolution, removeApproval, subscribe, retry, refreshStatus]);

  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}
