import { useEffect, useRef, useState, type ReactNode } from 'react';
import { errorMessage } from '../api';

/**
 * A setting that saves when flipped: a real switch, the current state in
 * words, and visible feedback (saving, saved, or why it couldn't).
 */
export function Toggle({ checked, onChange, title, on, off, disabled }: {
  checked: boolean;
  /** Saves the new value; resolves with the value the Seed now has. */
  onChange: (next: boolean) => Promise<boolean>;
  title: ReactNode;
  /** What "on" and "off" mean, in a sentence. */
  on: ReactNode;
  off: ReactNode;
  disabled?: boolean;
}) {
  const [value, setValue] = useState(checked);
  const [state, setState] = useState<'idle' | 'saving' | 'saved'>('idle');
  const [err, setErr] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => { if (state === 'idle') setValue(checked); }, [checked, state]);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const flip = async () => {
    const next = !value;
    setValue(next); // flip at once; put it back if saving fails
    setState('saving');
    setErr(null);
    try {
      setValue(await onChange(next));
      setState('saved');
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setState('idle'), 2000);
    } catch (e) {
      setValue(!next);
      setState('idle');
      setErr(errorMessage(e));
    }
  };

  return (
    <div className="toggle-row">
      <button type="button" role="switch" aria-checked={value} className={`toggle${value ? ' on' : ''}`}
        onClick={flip} disabled={disabled || state === 'saving'}>
        <span className="toggle-knob" />
      </button>
      <div className="toggle-text">
        <span className="toggle-title">
          {title}
          <span className={`toggle-state ${value ? 'is-on' : ''}`}>{value ? 'On' : 'Off'}</span>
          {state === 'saving' && <span className="toggle-feedback muted">Saving…</span>}
          {state === 'saved' && <span className="toggle-feedback saved" role="status">Saved ✓</span>}
        </span>
        <span className="small muted">{value ? on : off}</span>
        {err && <span className="error-text small" role="alert">Couldn't save: {err}</span>}
      </div>
    </div>
  );
}
