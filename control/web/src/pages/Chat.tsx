import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { api, errorMessage, isWaitingOnOwner, type Message } from '../api';
import { ApprovalCard } from '../components/ApprovalCard';
import { Composer } from '../components/Composer';
import { EvolutionCard } from '../components/EvolutionCard';
import { Markdown } from '../components/Markdown';
import { ErrorNote, Loading, clockTime, useLoad } from '../components/ui';
import { Logo } from '../components/Logo';
import { MindSetup } from '../components/MindSetup';
import { PointedAskCard } from '../components/PointedAsk';
import { parseAsk, splitAskMessage, type PointedAsk } from '../pointedAsk';
import type { Identity } from '../api';
import { useLive, useLiveEvent } from '../live';

function mergeMessages(prev: Message[], next: Message[]): Message[] {
  if (!next.length) return prev;
  const map = new Map<string, Message>();
  for (const m of prev) map.set(m.id, m);
  for (const m of next) map.set(m.id, m);
  return [...map.values()].sort((a, b) => (a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0));
}

function UserBubble({ content }: { content: string }) {
  const ask = splitAskMessage(content);
  if (!ask) return <div className="bubble">{content}</div>;
  return (
    <div className="bubble">
      {ask.words}
      <details className="bubble-pointed">
        <summary>What I pointed at</summary>
        <pre>{ask.pointed}</pre>
      </details>
    </div>
  );
}

function MessageView({ m, showCard, identity }: { m: Message; showCard: boolean; identity: Identity | null }) {
  if (m.role === 'system') {
    return (
      <div className="msg msg-system">
        <Markdown source={m.content} />
        {showCard && m.evolution_id && <EvolutionCard id={m.evolution_id} />}
      </div>
    );
  }
  if (m.role === 'user') {
    return (
      <div className="msg msg-user">
        <UserBubble content={m.content} />
        <div className="msg-time">{clockTime(m.created_at)}</div>
      </div>
    );
  }
  return (
    <div className="msg msg-seed">
      <div className="avatar" aria-hidden><Logo identity={identity} size={18} /></div>
      <div className="msg-body">
        {m.content.trim() && <Markdown source={m.content} />}
        {showCard && m.evolution_id && <EvolutionCard id={m.evolution_id} />}
        <div className="msg-time">{clockTime(m.created_at)}</div>
      </div>
    </div>
  );
}

export default function Chat() {
  const { status, thinking, approvals, evolutions, upsertEvolution } = useLive();
  const identity = status?.identity ?? null;
  const thinkingLabel = identity?.name ? `${identity.name} is thinking…` : 'Thinking…';
  // While an evolution waits on questions, the kernel routes the next chat message to it as the answer.
  const asking = useMemo(() => Object.values(evolutions).some(isWaitingOnOwner), [evolutions]);
  const placeholder = asking
    ? (identity?.name ? `Answer ${identity.name}'s question…` : 'Answer the question…')
    : (identity?.name ? `Message ${identity.name}…` : 'Message…');
  const [messages, setMessages] = useState<Message[]>([]);
  const [sendError, setSendError] = useState<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // Something my owner pointed at on a page of my organism (see PointedAsk).
  const [pointed, setPointed] = useState<PointedAsk | null>(null);
  useEffect(() => {
    const take = () => {
      if (!window.location.hash.startsWith('#ask=')) return;
      setPointed(parseAsk(window.location.hash));
      window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search);
    };
    take();
    window.addEventListener('hashchange', take);
    return () => window.removeEventListener('hashchange', take);
  }, []);
  const stick = useRef(true);

  const load = useLoad(() => api.messages(200), []);
  useEffect(() => { if (load.data) setMessages((prev) => mergeMessages(prev, load.data!)); }, [load.data]);

  // Seed the evolution store in one request so inline cards don't each fetch.
  useEffect(() => {
    api.evolutions().then((list) => list.forEach(upsertEvolution)).catch(() => {});
  }, [upsertEvolution, load.data]);

  useLiveEvent('message', (m) => setMessages((prev) => mergeMessages(prev, [m])));

  const send = useCallback(async (content: string) => {
    setSendError(null);
    try {
      const m = await api.sendMessage(content);
      stick.current = true;
      setMessages((prev) => mergeMessages(prev, [m]));
      return true;
    } catch (e) {
      setSendError(errorMessage(e));
      return false;
    }
  }, []);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [messages, thinking, approvals.length]);

  // Cards render once, on the first message that references an evolution.
  const cardOwners = useMemo(() => {
    const seen = new Set<string>();
    const owners = new Set<string>();
    for (const m of messages) {
      if (m.evolution_id && !seen.has(m.evolution_id)) { seen.add(m.evolution_id); owners.add(m.id); }
    }
    return owners;
  }, [messages]);

  const loadedEmpty = load.data !== null && messages.length === 0;
  const firstRun = loadedEmpty && status !== null && !status.purpose;
  const needsMind = status !== null && !status.model.configured;

  if (load.error && !load.data) {
    return <div className="page"><ErrorNote error={load.error} onRetry={load.reload} /></div>;
  }
  if (!load.data && load.loading) return <div className="page"><Loading /></div>;

  if (firstRun) {
    return (
      <div className="hero">
        <div className="hero-inner">
          <div className="hero-mark"><Logo identity={identity} size={36} /></div>
          {identity?.name && <h1 className="hero-title">{identity.name}</h1>}
          {needsMind ? <MindSetup /> : (
            <>
              <p className="hero-line">I don't have a purpose yet.</p>
              <p className="hero-question">What should I become?</p>
              {approvals.map((a) => <ApprovalCard key={a.id} approval={a} />)}
              <Composer onSend={send} autoFocus large placeholder="Become a…" />
              {sendError && <div className="error-text small">{sendError}</div>}
              {thinking && <div className="thinking hero-thinking"><Dots /> {thinkingLabel}</div>}
            </>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="chat">
      <div className="chat-scroll" ref={scroller} onScroll={onScroll}>
        <div className="chat-col">
          {loadedEmpty && (
            <div className="chat-empty muted">
              {status?.purpose ? <>Purpose: <span className="fg">{status.purpose}</span></> : 'No messages yet.'}
            </div>
          )}
          {messages.map((m) => <MessageView key={m.id} m={m} showCard={cardOwners.has(m.id)} identity={identity} />)}
          {thinking && (
            <div className="msg msg-seed">
              <div className="avatar" aria-hidden><Logo identity={identity} size={18} /></div>
              <div className="thinking"><Dots /> {thinkingLabel}</div>
            </div>
          )}
        </div>
      </div>
      <div className="chat-bottom">
        <div className="chat-col">
          {approvals.map((a) => <ApprovalCard key={a.id} approval={a} />)}
          {needsMind ? <MindSetup compact /> : (
            <>
              {pointed && (
                <PointedAskCard
                  ask={pointed}
                  onCancel={() => setPointed(null)}
                  onSend={async (content) => { const ok = await send(content); if (ok) setPointed(null); return ok; }}
                />
              )}
              {sendError && <div className="error-text small">{sendError}</div>}
              <Composer onSend={send} autoFocus placeholder={placeholder} />
              <div className="composer-hint">Enter to send · Shift+Enter for a new line</div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Dots() {
  return <span className="dots" aria-hidden><i /><i /><i /></span>;
}
