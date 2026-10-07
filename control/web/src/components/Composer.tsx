import { useEffect, useRef, useState, type KeyboardEvent } from 'react';

type Props = {
  onSend: (text: string) => Promise<boolean>;
  placeholder?: string;
  autoFocus?: boolean;
  disabled?: boolean;
  large?: boolean;
};

export function Composer({ onSend, placeholder, autoFocus, disabled, large }: Props) {
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 240) + 'px';
  }, [text]);

  useEffect(() => { if (autoFocus) ref.current?.focus(); }, [autoFocus]);

  const submit = async () => {
    const value = text.trim();
    if (!value || sending || disabled) return;
    setSending(true);
    setText('');
    const ok = await onSend(value);
    setSending(false);
    if (!ok) setText(value);
    ref.current?.focus();
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className={`composer${large ? ' composer-large' : ''}`}>
      <textarea
        ref={ref}
        rows={1}
        value={text}
        placeholder={placeholder ?? 'Message…'}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        disabled={disabled}
        aria-label="Message"
      />
      <button className="composer-send" onClick={submit} disabled={!text.trim() || sending || disabled} aria-label="Send">
        {sending ? <span className="spinner" /> : (
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden><path d="M8 13V3M3.5 7.5 8 3l4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>
        )}
      </button>
    </div>
  );
}
