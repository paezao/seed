import { useState, type KeyboardEvent } from 'react';
import { api, errorMessage, type Clarification, type Evolution, type Question } from '../api';

/** Compose the answer text sent to the kernel from picked options and free text. */
export function composeAnswer(questions: Question[], picks: Record<number, string>, text: string): string {
  const free = text.trim();
  if (questions.length <= 1) return [picks[0], free].filter(Boolean).join('\n\n');
  const lines = questions.flatMap((q, i) => (picks[i] ? [`${i + 1}. ${q.question} → ${picks[i]}`] : []));
  return [lines.join('\n'), free].filter(Boolean).join('\n\n');
}

/**
 * "I need your input": the questions an evolution is waiting on, with the
 * suggested answers as chips. With one question, a chip answers immediately;
 * the owner can instead open a text box (then chips toggle and combine with
 * the text). With several, chips toggle and one "Send answer" sends it all.
 *
 * Key this by the question round so a new round starts fresh.
 */
export function QuestionPrompt({ evolution }: { evolution: Evolution }) {
  const qs = evolution.questions ?? [];
  const single = qs.length === 1;
  const hasOptions = qs.some((q) => q.options?.length);
  const [picks, setPicks] = useState<Record<number, string>>({});
  const [text, setText] = useState('');
  const [ownWords, setOwnWords] = useState(false);
  const [sending, setSending] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // One question with suggestions: a chip is the answer, unless the owner opens the text box.
  const quick = single && hasOptions && !ownWords;
  const showText = !quick;
  const answer = composeAnswer(qs, picks, text);
  const busy = sending !== null || sent;

  const send = async (value: string) => {
    if (!value.trim() || busy) return;
    setSending(value);
    setErr(null);
    try {
      await api.answer(evolution.id, value);
      setSent(true);
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setSending(null);
    }
  };

  const onChip = (qi: number, opt: string) => {
    if (quick) {
      if (busy) return;
      setPicks({ [qi]: opt });
      void send(opt);
      return;
    }
    setPicks((p) => {
      const next = { ...p };
      if (next[qi] === opt) delete next[qi]; else next[qi] = opt;
      return next;
    });
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send(answer);
    }
  };

  return (
    <section className={`ask${sent ? ' ask-sent' : ''}`} aria-label="Questions waiting for your answer">
      <div className="ask-head">
        <span className="ask-dot" aria-hidden />
        <span className="ask-title">I need your input</span>
        <span className="ask-sub">{sent ? "Thank you. I'm carrying on." : "I'll carry on as soon as you answer."}</span>
      </div>

      <ol className={`ask-list${single ? ' single' : ''}`}>
        {qs.map((q, qi) => (
          <li key={qi} className="ask-q">
            <div className="ask-question">{q.question}</div>
            {q.why && <div className="ask-why">{q.why}</div>}
            {!!q.options?.length && (
              <div className="ask-options">
                {q.options.map((opt, oi) => {
                  const picked = picks[qi] === opt;
                  const pending = quick && sending === opt;
                  return (
                    <button
                      key={oi}
                      type="button"
                      className={`ask-chip${picked ? ' picked' : ''}${oi === 0 ? ' recommended' : ''}`}
                      onClick={() => onChip(qi, opt)}
                      disabled={busy}
                      aria-pressed={quick ? undefined : picked}
                      title={quick ? 'Send this answer' : undefined}
                    >
                      {pending && <span className="spinner" />}
                      <span>{opt}</span>
                      {oi === 0 && <span className="ask-rec">recommended</span>}
                    </button>
                  );
                })}
              </div>
            )}
          </li>
        ))}
      </ol>

      {single && hasOptions && !sent && (
        <button type="button" className="link-btn ask-toggle" onClick={() => setOwnWords((o) => !o)} disabled={busy}>
          {ownWords ? 'Just pick an option' : 'or answer in your own words'}
        </button>
      )}

      {showText && !sent && (
        <div className="ask-compose">
          <textarea
            rows={2}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKey}
            placeholder="Or tell me in your own words…"
            disabled={busy}
            aria-label="Your answer"
          />
          <div className="ask-actions">
            <span className="ask-hint">{qs.length > 1 && Object.keys(picks).length ? `${Object.keys(picks).length} of ${qs.length} picked` : 'Enter to send'}</span>
            <button type="button" className="btn btn-primary btn-sm" onClick={() => void send(answer)} disabled={!answer || busy}>
              {sending !== null ? <><span className="spinner" /> Sending…</> : 'Send answer'}
            </button>
          </div>
        </div>
      )}

      {err && <div className="ask-error" role="alert">{err}</div>}
    </section>
  );
}

/** Earlier question/answer rounds ("You said: …"); collapsed behind a toggle on cards. */
export function Clarifications({ items, collapsed }: { items?: Clarification[]; collapsed?: boolean }) {
  if (!items?.length) return null;
  const body = (
    <div className="clar-list">
      {items.map((c, i) => (
        <div key={i} className="clar-round">
          <ul className="clar-qs">{c.questions.map((q, j) => <li key={j}>{q.question}</li>)}</ul>
          <div className="clar-answer"><span className="clar-said">You said:</span> <span className="prewrap">{c.answer}</span></div>
        </div>
      ))}
    </div>
  );
  if (!collapsed) return body;
  return (
    <details className="clar-details">
      <summary>What you told me{items.length > 1 ? ` (${items.length})` : ''}</summary>
      {body}
    </details>
  );
}
