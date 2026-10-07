import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { api, errorMessage, type ModelConfig } from '../api';
import { Logo } from '../components/Logo';
import { ModelForm } from '../components/ModelForm';
import { ErrorNote, Loading, useLoad } from '../components/ui';
import { useLive } from '../live';

type Step = 'hello' | 'brain' | 'waking' | 'become';

const IDEAS = ['a todo list', 'a recipe book', 'a habit tracker', 'a tiny CRM', 'a reading log'];

/**
 * The first-run experience, fullscreen: the Seed introduces itself, asks for
 * a brain (provider key + model), wakes up, and asks what it should become.
 * The normal control plane appears once the owner has said what they want.
 */
export default function Welcome({ onDone }: { onDone: () => void }) {
  const { status } = useLive();
  const identity = status?.identity ?? null;
  // Decided once: a Seed that already had a brain when this opened says "hello again".
  const [hadBrain] = useState(() => !!status?.model.configured);
  const [step, setStep] = useState<Step>(hadBrain ? 'become' : 'hello');
  const toBecome = useCallback(() => setStep('become'), []);

  return (
    <div className={`welcome welcome-${step}`}>
      <div className="welcome-glow" aria-hidden />
      <main className="welcome-stage">
        <div className={`welcome-sprout${step === 'waking' ? ' growing' : ''}`}>
          <Logo identity={identity} size={step === 'hello' ? 72 : 56} />
        </div>
        {step === 'hello' && <Hello onNext={() => setStep('brain')} />}
        {step === 'brain' && <Brain onBack={() => setStep('hello')} onSaved={() => setStep('waking')} />}
        {step === 'waking' && <Waking onDone={toBecome} />}
        {step === 'become' && <Become greet={hadBrain} onDone={onDone} />}
      </main>
      <button type="button" className="welcome-skip" onClick={onDone}>Skip to the control plane →</button>
    </div>
  );
}

function Hello({ onNext }: { onNext: () => void }) {
  return (
    <div className="welcome-copy" key="hello">
      <h1 className="w-line w-1">Hello. I'm a Seed.</h1>
      <p className="w-line w-2">Right now I'm almost nothing: a tiny kernel that knows how to grow.</p>
      <p className="w-line w-3">
        Tell me what to become and I'll rewrite myself into it, and keep growing whenever you ask for more.
      </p>
      <p className="w-line w-4 w-emph">But first, I need a brain.</p>
      <div className="w-line w-5">
        <button type="button" className="welcome-cta" onClick={onNext} autoFocus>
          <span aria-hidden>🧠</span> Give me a brain
        </button>
      </div>
    </div>
  );
}

function Brain({ onBack, onSaved }: { onBack: () => void; onSaved: (cfg: ModelConfig) => void }) {
  const load = useLoad(() => api.model(), []);
  return (
    <div className="welcome-copy" key="brain">
      <h1 className="w-line w-1">Pick a brain for me.</h1>
      <p className="w-line w-2">
        I think with a language model through OpenRouter. One key opens any model. Your key stays on this
        machine (<code>~/.config/seed</code>), never in my code.
      </p>
      <div className="w-line w-3 welcome-card">
        {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
        {!load.data && load.loading && <Loading />}
        {load.data && (
          <ModelForm config={load.data} onSaved={onSaved} submitLabel="Plug it in" busyLabel="Plugging in…" onCancel={onBack} cancelLabel="← Back" />
        )}
      </div>
    </div>
  );
}

function Waking({ onDone }: { onDone: () => void }) {
  const [awake, setAwake] = useState(false);
  useEffect(() => {
    const a = window.setTimeout(() => setAwake(true), 1400);
    const b = window.setTimeout(onDone, 3000);
    return () => { window.clearTimeout(a); window.clearTimeout(b); };
  }, [onDone]);
  return (
    <div className="welcome-copy" key="waking" aria-live="polite">
      {!awake ? (
        <p className="w-line w-1 waking-dots">Waking up<span>.</span><span>.</span><span>.</span></p>
      ) : (
        <h1 className="w-line w-1">Oh! I can think.</h1>
      )}
    </div>
  );
}

function Become({ greet, onDone }: { greet: boolean; onDone: () => void }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { input.current?.focus(); }, []);

  const send = async () => {
    const content = text.trim();
    if (!content || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.sendMessage(content);
      onDone();
    } catch (e) {
      setErr(errorMessage(e));
      setBusy(false);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void send(); }
  };

  return (
    <div className="welcome-copy" key="become">
      {greet && <p className="w-line w-1">Hello again. I have a brain, but no purpose yet.</p>}
      <h1 className={`w-line ${greet ? 'w-2' : 'w-1'} welcome-question`}>What should I become?</h1>
      <div className={`w-line ${greet ? 'w-3' : 'w-2'} welcome-ask`}>
        <textarea
          ref={input}
          rows={2}
          value={text}
          placeholder="Become a todo application. I need to create, complete and delete tasks."
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKey}
          disabled={busy}
          aria-label="What should I become?"
        />
        <button type="button" className="welcome-cta" onClick={() => void send()} disabled={!text.trim() || busy}>
          {busy ? 'Growing…' : 'Grow'}
        </button>
      </div>
      <div className={`w-line ${greet ? 'w-4' : 'w-3'} welcome-ideas`}>
        <span className="muted">or try</span>
        {IDEAS.map((idea) => (
          <button key={idea} type="button" className="chip" onClick={() => { setText(`Become ${idea}.`); input.current?.focus(); }}>
            {idea}
          </button>
        ))}
      </div>
      {err && <p className="welcome-error">{err}</p>}
    </div>
  );
}
