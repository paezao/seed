import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from 'react';
import {
  api, errorMessage, isActive, isWaitingOnOwner,
  type Evolution, type EvolutionEvent, type Identity, type Message, type ModelConfig,
} from '../api';
import { ActivityTicker } from '../components/ActivityTicker';
import { brainProvider, CopyCommand, keyEnvOf, restartCommand } from '../components/Brain';
import { Markdown } from '../components/Markdown';
import { ModelForm } from '../components/ModelForm';
import { QuestionPrompt } from '../components/Questions';
import { SproutPlant } from '../components/Sprout';
import { ErrorNote, Loading, useLoad } from '../components/ui';
import { useEvolution, useLive, useLiveEvent } from '../live';
import { growth, type GrowthMood, type GrowthStage } from '../phases';

type Step = 'brain' | 'ask' | 'grow';
type Plant = { stage: GrowthStage; mood: GrowthMood };

/**
 * The first-run experience, fullscreen and without control-plane chrome:
 * (brain) if no key was passed at start, how to give me one;
 * (ask) a quick hello that leads straight into "What should I become?";
 * (grow) the wish is planted and the real evolution is followed live until
 * its plan is ready, then the owner is handed to the control plane.
 */
export default function Welcome({ fresh, onDone }: { fresh: boolean; onDone: () => void }) {
  const { status } = useLive();
  const identity = status?.identity ?? null;
  const configured = !!status?.model.configured;
  const reduced = useReducedMotion();

  const [step, setStep] = useState<Step>(() => (configured ? 'ask' : 'brain'));
  const [woke, setWoke] = useState(false);
  const [sent, setSent] = useState<Message | null>(null);

  // The plant is shared by every step so it can morph from one to the next.
  const [growPlant, setGrowPlant] = useState<Plant>({ stage: 0, mood: 'growing' });
  const [typed, setTyped] = useState(0);
  const [keystrokes, setKeystrokes] = useState(0);
  const [burst, setBurst] = useState(false);

  // A brain arrived (lent here, or the Seed was restarted with a key): carry on, once.
  const proceeded = useRef(false);
  const gotBrain = useCallback(() => {
    if (proceeded.current) return;
    proceeded.current = true;
    if (fresh) { setWoke(true); setStep('ask'); } else onDone();
  }, [fresh, onDone]);
  useEffect(() => { if (step === 'brain' && configured) gotBrain(); }, [step, configured, gotBrain]);

  const planted = (m: Message) => {
    setSent(m);
    setStep('grow');
    setBurst(true);
  };
  useEffect(() => {
    if (!burst) return;
    const t = window.setTimeout(() => setBurst(false), 1500);
    return () => window.clearTimeout(t);
  }, [burst]);

  const plant: Plant = step === 'brain' ? { stage: 0, mood: 'waiting' } : step === 'ask' ? { stage: 1, mood: 'growing' } : growPlant;
  const lean = step === 'ask' ? 1 + Math.min(typed, 160) / 900 : 1;
  const plantLabel = step === 'brain' ? 'A dormant seed' : step === 'ask' ? 'A little sprout, listening' : undefined;

  return (
    <div className={`welcome at-${step}`}>
      <div className="wl-glow" aria-hidden />
      {!reduced && <Fireflies />}
      <main className="welcome-stage">
        <div className="wl-plant" style={{ '--lean': lean } as CSSProperties}>
          <div className={`wl-plant-drop${step === 'grow' ? ' planting' : ''}`}>
            <div className={`wl-plant-wig ${keystrokes ? `wig-${keystrokes % 2}` : ''}`}>
              <SproutPlant stage={plant.stage} mood={plant.mood} size={168} celebrate={burst} label={plantLabel} />
            </div>
          </div>
        </div>
        {step === 'brain' && <Brain onSaved={(cfg) => { if (cfg.configured) gotBrain(); }} />}
        {step === 'ask' && (
          <Ask
            identity={identity}
            woke={woke}
            reduced={reduced}
            onType={(len) => { setTyped(len); setKeystrokes((k) => k + 1); }}
            onSent={planted}
          />
        )}
        {step === 'grow' && sent && <Grow sent={sent} onPlant={setGrowPlant} onDone={onDone} />}
      </main>
      {step !== 'grow' && (
        <button type="button" className="welcome-skip" onClick={onDone}>Skip to the control plane →</button>
      )}
    </div>
  );
}

/* ── no key: "I need a brain to think." ─────────────────────────────── */

function Brain({ onSaved }: { onSaved: (cfg: ModelConfig) => void }) {
  const load = useLoad(() => api.model(), []);
  const provider = brainProvider(load.data);
  const [lending, setLending] = useState(false);
  const env = keyEnvOf(provider);

  return (
    <div className="wl-copy wl-brain-copy" key="brain">
      <h1 className="wl-rise">I need a brain to think.</h1>
      <p className="wl-rise wl-d1">
        I think with a language model{provider ? <> through {provider.label}</> : null}, but no key was passed
        when I started. Restart me with one:
      </p>
      <div className="wl-rise wl-d2">
        <CopyCommand command={restartCommand(provider)} label="Command to restart me with a key" />
        <p className="wl-cmd-note">
          with <code>{env}</code> set in your shell.
          {provider?.keys_url && <> <a className="wl-link" href={provider.keys_url} target="_blank" rel="noreferrer">Get a key ↗</a></>}
        </p>
      </div>
      <div className="wl-rise wl-d3">
        {!lending ? (
          <button type="button" className="link-btn wl-lend" onClick={() => setLending(true)} disabled={!load.data && !load.error}>
            or lend me a key for this session
          </button>
        ) : (
          <div className="welcome-card">
            {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
            {!load.data && load.loading && <Loading />}
            {load.data && (
              <ModelForm
                config={load.data}
                onSaved={onSaved}
                submitLabel="Wake up"
                busyLabel="Waking up…"
                onCancel={() => setLending(false)}
                cancelLabel="Never mind"
                autoFocusKey
              />
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/* ── hello → "What should I become?" ─────────────────────────────────── */

const WISHES = [
  'Become a recipe book for my family…',
  'Become a tiny CRM for my bakery…',
  'Become a habit tracker that nudges me…',
  'Become a reading log with notes and quotes…',
  'Become a shared shopping list for our flat…',
];

const IDEAS: { emoji: string; label: string; text: string }[] = [
  { emoji: '📖', label: 'Recipe book', text: 'Become a recipe book for my family: we add recipes with ingredients and steps, tag them, and search them.' },
  { emoji: '✅', label: 'Todo list', text: 'Become a todo list: I can add, complete and delete tasks, with due dates.' },
  { emoji: '🥐', label: 'Bakery CRM', text: 'Become a tiny CRM for my bakery: customers, their usual orders, and notes.' },
  { emoji: '🌱', label: 'Habit tracker', text: 'Become a habit tracker: I define daily habits, check them off, and see my streaks.' },
  { emoji: '📚', label: 'Reading log', text: 'Become a reading log: books I read, my ratings, notes and favourite quotes.' },
];

function Ask({ identity, woke, reduced, onType, onSent }: {
  identity: Identity | null;
  woke: boolean;
  reduced: boolean;
  onType: (len: number) => void;
  onSent: (m: Message) => void;
}) {
  const name = identity?.name?.trim();
  const lines = useMemo(() => [
    woke ? 'Oh! I can think now. Thank you.' : `Hello. I'm ${name && name !== 'Seed' ? name : 'a Seed'}.`,
    "Right now I'm almost nothing — a little kernel that knows how to grow.",
    "Tell me what to become, and I'll rewrite myself into it.",
  ], [woke, name]);
  const intro = useTypewriter(lines, !reduced);

  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const ghost = useCyclingPlaceholder(WISHES, intro.done && !text && !reduced);

  // The box is focused from the start: typing during the intro skips it and lands in the box.
  useEffect(() => { input.current?.focus(); }, []);
  const { done: introDone, skip: skipIntro } = intro;
  useEffect(() => {
    if (introDone) return;
    window.addEventListener('keydown', skipIntro);
    window.addEventListener('pointerdown', skipIntro);
    return () => { window.removeEventListener('keydown', skipIntro); window.removeEventListener('pointerdown', skipIntro); };
  }, [introDone, skipIntro]);

  const change = (v: string) => { setText(v); onType(v.length); };

  const send = async () => {
    const content = text.trim();
    if (!content || busy) return;
    setBusy(true);
    setErr(null);
    try {
      onSent(await api.sendMessage(content));
    } catch (e) {
      setErr(errorMessage(e));
      setBusy(false);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); void send(); }
  };

  return (
    <div className="wl-copy" key="ask">
      <div className="wl-hello">
        {lines.map((l, i) => {
          const shown = intro.shown[i] ?? 0;
          const typing = !intro.done && shown > 0 && shown < l.length;
          const Tag = i === 0 ? 'h1' : 'p';
          return (
            <Tag key={i} className={`wl-hello-line wl-hello-${i}${shown > 0 ? ' on' : ''}`}>
              <span className="sr-only">{l}</span>
              <span aria-hidden>
                {l.slice(0, shown)}
                {typing && <span className="wl-caret" />}
                {/* reserve the line's final height so nothing jumps while it types */}
                <span className="wl-rest">{l.slice(shown)}</span>
              </span>
            </Tag>
          );
        })}
      </div>

      <div className={`wl-ask${intro.done ? ' in' : ''}`}>
        <h2 className="wl-question" id="wl-question">What should I become?</h2>
        <div className="wl-box">
          <div className="wl-field">
            <textarea
              ref={input}
              rows={2}
              value={text}
              onChange={(e) => change(e.target.value)}
              onKeyDown={onKey}
              disabled={busy}
              aria-labelledby="wl-question"
              aria-describedby="wl-hint"
              placeholder={reduced ? WISHES[0] : undefined}
            />
            {!reduced && !text && (
              <span className="wl-ghost" aria-hidden>{ghost}<span className="wl-caret soft" /></span>
            )}
          </div>
          <button type="button" className="welcome-cta wl-grow-btn" onClick={() => void send()} disabled={!text.trim() || busy}>
            <span aria-hidden>🌱</span> {busy ? 'Planting…' : 'Grow'}
          </button>
        </div>
        <div className="wl-ideas" role="group" aria-label="Ideas to start from">
          {IDEAS.map((idea, i) => (
            <button
              key={idea.label}
              type="button"
              className="wl-idea"
              style={{ '--i': i } as CSSProperties}
              onClick={() => { change(idea.text); input.current?.focus(); }}
              disabled={busy}
            >
              <span aria-hidden>{idea.emoji}</span> {idea.label}
            </button>
          ))}
        </div>
        <p className="wl-hint" id="wl-hint">Enter to grow · Shift+Enter for a new line</p>
        {err && <p className="wl-error" role="alert">{err}</p>}
      </div>
    </div>
  );
}

/* ── after "Grow": follow the real evolution live ───────────────────── */

const STEP_MS = 380;
const LINGER_MS = 5500;

function Grow({ sent, onPlant, onDone }: { sent: Message; onPlant: (p: Plant) => void; onDone: () => void }) {
  const { status, thinking, upsertEvolution, resync } = useLive();
  const [evoId, setEvoId] = useState<string | null>(null);
  const [reply, setReply] = useState<Message | null>(null);
  const [events, setEvents] = useState<EvolutionEvent[]>([]);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { heading.current?.focus(); }, []);

  // Anything the kernel creates in response carries a timestamp after the wish (allow some clock slack).
  const since = useMemo(() => Date.parse(sent.created_at) - 2000, [sent.created_at]);
  const after = useCallback((iso: string) => !(Date.parse(iso) < since), [since]);

  const consider = useCallback((m: Message) => {
    if (m.role === 'user' || m.id === sent.id || !after(m.created_at)) return;
    if (m.evolution_id) setEvoId((id) => id ?? m.evolution_id!);
    if (m.role === 'seed' && m.kind !== 'report') setReply((r) => r ?? m);
  }, [after, sent.id]);
  const considerEvolution = useCallback((e: Evolution) => {
    if (e.kind === 'evolve' && after(e.created_at)) setEvoId((id) => id ?? e.id);
  }, [after]);

  // The chat starts the evolution (an `evolution` event), then replies (a `message` carrying its id).
  useLiveEvent('evolution', considerEvolution);
  useLiveEvent('message', consider);
  const active = status?.active_evolution;
  useEffect(() => { if (active) considerEvolution(active); }, [active, considerEvolution]);

  // In case an event slipped by (reconnects), look until something turns up.
  const found = !!evoId || !!reply;
  useEffect(() => {
    if (evoId && reply) return;
    const look = () => {
      if (!evoId) api.evolutions().then((list) => list.slice().reverse().forEach(considerEvolution)).catch(() => {});
      api.messages(20).then((list) => list.forEach(consider)).catch(() => {});
    };
    if (resync) look();
    const t = window.setInterval(look, found ? 6000 : 3000);
    return () => window.clearInterval(t);
  }, [evoId, reply, found, resync, consider, considerEvolution]);

  // The evolution's events so far, then live.
  useEffect(() => {
    if (!evoId) return;
    let cancelled = false;
    api.evolution(evoId).then((r) => {
      if (cancelled) return;
      upsertEvolution(r.evolution);
      setEvents((prev) => mergeEvents(prev, r.events));
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [evoId, upsertEvolution, resync]);
  useLiveEvent('evolution_event', (ev) => { if (ev.evolution_id === evoId) setEvents((prev) => mergeEvents(prev, [ev])); });

  const evo = useEvolution(evoId ?? undefined);
  const plan = evo?.plan ?? null;
  const asking = isWaitingOnOwner(evo);
  const stopped = !!evo && (evo.status === 'failed' || evo.status === 'cancelled' || evo.status === 'rolled_back');
  const working = !!evo && isActive(evo.status) && !asking;
  const chatOnly = !!reply && !evoId;

  // The plant follows the evolution; it waits as a planted seed until then.
  const plant: Plant = useMemo(() => {
    if (!evo) return { stage: chatOnly ? 1 : 0, mood: 'growing' };
    const g = growth(evo);
    return { stage: Math.max(g.stage, plan ? 2 : 1) as GrowthStage, mood: g.mood };
  }, [evo, plan, chatOnly]);
  useEffect(() => { onPlant(plant); }, [plant.stage, plant.mood]); // eslint-disable-line react-hooks/exhaustive-deps

  // Once the plan is ready and has been on screen for a moment, hand over to the control plane.
  const steps = plan?.steps ?? [];
  const autoMs = plan && !asking && !stopped ? 900 + steps.length * STEP_MS + LINGER_MS : 0;
  useEffect(() => {
    if (!autoMs) return;
    const t = window.setTimeout(onDone, autoMs);
    return () => window.clearTimeout(t);
  }, [autoMs, onDone]);

  const nq = evo?.questions?.length ?? 0;

  return (
    <div className="wl-copy wl-growing" key="grow">
      <p className="wl-wish" title={sent.content}>“{sent.content}”</p>
      <h1 className="wl-grow-title" ref={heading} tabIndex={-1}>{chatOnly ? 'Let’s talk it through.' : 'Here I go.'}</h1>

      <div className="wl-status" aria-live="polite">
        <ul className="wl-lines">
          <StatusLine key="read" state={found ? 'done' : 'active'}>
            {found ? 'I read what you want.' : thinking ? 'Reading what you want…' : 'Taking in what you want…'}
          </StatusLine>
          {evo && (asking
            ? <StatusLine key="ask" state="wait">{nq === 1 ? 'Before I plan, one question for you.' : 'Before I plan, a few questions for you.'}</StatusLine>
            : plan
              ? <StatusLine key="plan" state="done">I have a plan.</StatusLine>
              : !stopped && <StatusLine key="plan" state="active">Planning my new shape…</StatusLine>)}
        </ul>

        {plan && (
          <section className="wl-plan" aria-label="My plan">
            <h2 className="wl-plan-title">{plan.title}</h2>
            {plan.summary && <p className="wl-plan-summary">{plan.summary}</p>}
            {steps.length > 0 && (
              <ol className="wl-steps">
                {steps.map((s, i) => (
                  <li key={i} style={{ '--i': i } as CSSProperties}>
                    <span className="wl-step-n" aria-hidden>{i + 1}</span>
                    <span>{s.title}</span>
                  </li>
                ))}
              </ol>
            )}
            {plan.stages && plan.stages.length > 1 && plan.stage && (
              <p className="wl-plan-stage">Stage {plan.stage} of {plan.stages.length} — I'll grow the rest step by step.</p>
            )}
          </section>
        )}

        <ul className="wl-lines">
          {working && plan && <StatusLine key="grow" state="active">Growing into it now.</StatusLine>}
          {evo?.status === 'complete' && <StatusLine key="done" state="done">Done. I've become something new.</StatusLine>}
          {evo && stopped && (
            <StatusLine key="stop" state="fail">
              {evo.status === 'failed' ? `I stumbled${evo.error ? `: ${evo.error}` : '.'}` : 'This evolution was stopped.'}
            </StatusLine>
          )}
        </ul>
      </div>

      {evo && working && <div className="wl-ticker"><ActivityTicker events={events} status={evo.status} /></div>}

      {evo && asking && (
        <div className="wl-questions">
          <QuestionPrompt key={`${evo.id}:${evo.clarifications?.length ?? 0}`} evolution={evo} />
        </div>
      )}

      {reply && reply.content.trim() && (
        <div className="wl-reply">
          <Markdown source={reply.content} />
        </div>
      )}

      <div className="wl-actions">
        <button
          type="button"
          className={`welcome-cta wl-continue${autoMs ? ' auto' : ''}`}
          style={autoMs ? ({ '--auto': `${autoMs}ms` } as CSSProperties) : undefined}
          onClick={onDone}
        >
          {chatOnly ? 'Continue in chat →' : 'Watch me grow in my control plane →'}
        </button>
        {autoMs > 0 && <p className="wl-hint">Taking you there in a moment…</p>}
      </div>
    </div>
  );
}

function StatusLine({ state, children }: { state: 'done' | 'active' | 'wait' | 'fail'; children: ReactNode }) {
  return (
    <li className={`wl-line-item is-${state}`}>
      <span className="wl-line-mark" aria-hidden>{state === 'done' ? '✓' : state === 'fail' ? '✕' : state === 'wait' ? '?' : ''}</span>
      <span>{children}</span>
    </li>
  );
}

function mergeEvents(prev: EvolutionEvent[], next: EvolutionEvent[]): EvolutionEvent[] {
  if (!next.length) return prev;
  const map = new Map<number, EvolutionEvent>();
  for (const e of prev) map.set(e.id, e);
  for (const e of next) map.set(e.id, e);
  return [...map.values()].sort((a, b) => a.id - b.id);
}

/* ── ambience & motion helpers ──────────────────────────────────────── */

/** Soft drifting pollen in the accent color: pure CSS transforms, decorative. */
function Fireflies({ count = 18 }: { count?: number }) {
  const flies = useMemo(() => {
    let seed = 7;
    const rnd = () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };
    return Array.from({ length: count }, () => ({
      left: `${(rnd() * 100).toFixed(1)}%`,
      top: `${(20 + rnd() * 80).toFixed(1)}%`,
      '--dx': `${Math.round((rnd() - 0.5) * 160)}px`,
      '--dy': `${-Math.round(80 + rnd() * 220)}px`,
      '--dur': `${(10 + rnd() * 12).toFixed(1)}s`,
      '--delay': `${(-rnd() * 20).toFixed(1)}s`,
      '--size': `${(2 + rnd() * 3.5).toFixed(1)}px`,
      '--peak': (0.35 + rnd() * 0.5).toFixed(2),
    }));
  }, [count]);
  return (
    <div className="wl-flies" aria-hidden>
      {flies.map((f, i) => <i key={i} style={f as CSSProperties} />)}
    </div>
  );
}

function useReducedMotion(): boolean {
  const q = '(prefers-reduced-motion: reduce)';
  const [reduced, setReduced] = useState(() => window.matchMedia(q).matches);
  useEffect(() => {
    const mq = window.matchMedia(q);
    const on = () => setReduced(mq.matches);
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, []);
  return reduced;
}

/**
 * Types lines one after another within ~2.2s in total; `skip` shows everything.
 * Disabled (everything shown at once) when `enabled` is false.
 */
function useTypewriter(lines: string[], enabled: boolean) {
  const total = lines.reduce((n, l) => n + l.length, 0);
  const pause = 160;
  const charMs = Math.min(26, (2200 - pause * (lines.length - 1)) / Math.max(1, total));
  const [elapsed, setElapsed] = useState(enabled ? 0 : Infinity);
  const end = total * charMs + pause * (lines.length - 1);
  const done = elapsed >= end;
  const raf = useRef(0);

  useEffect(() => {
    if (!enabled) { setElapsed(Infinity); return; }
    setElapsed(0);
    const start = performance.now();
    const tick = (now: number) => {
      const e = now - start;
      setElapsed(e);
      if (e < end) raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf.current);
  }, [enabled, end, lines]);

  const shown = useMemo(() => {
    let t = elapsed;
    return lines.map((l) => {
      if (t <= 0) return 0;
      const n = Math.min(l.length, Math.floor(t / charMs) + 1);
      t -= l.length * charMs + pause;
      return n;
    });
  }, [elapsed, lines, charMs]);

  const skip = useCallback(() => { cancelAnimationFrame(raf.current); setElapsed(Infinity); }, []);
  return useMemo(() => ({ shown, done, skip }), [shown, done, skip]);
}

/** Types and erases example wishes, one after another, while `active`. */
function useCyclingPlaceholder(examples: string[], active: boolean): string {
  const [text, setText] = useState('');
  useEffect(() => {
    if (!active) { setText(''); return; }
    let i = 0;
    let n = 0;
    let erasing = false;
    let t = 0;
    const step = () => {
      const full = examples[i % examples.length];
      if (!erasing) {
        n += 1;
        setText(full.slice(0, n));
        if (n >= full.length) { erasing = true; t = window.setTimeout(step, 1700); return; }
        t = window.setTimeout(step, 34 + Math.random() * 40);
      } else {
        n -= 2;
        setText(full.slice(0, Math.max(0, n)));
        if (n <= 0) { erasing = false; n = 0; i += 1; t = window.setTimeout(step, 380); return; }
        t = window.setTimeout(step, 16);
      }
    };
    t = window.setTimeout(step, 500);
    return () => window.clearTimeout(t);
  }, [examples, active]);
  return text;
}
