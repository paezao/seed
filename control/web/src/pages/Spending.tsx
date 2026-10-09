import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, usd, type Spending as SpendingData, type SpendTotal } from '../api';
import { Badge, ErrorNote, Loading, PageHeader, useLoad } from '../components/ui';
import { useLive, useLiveEvent } from '../live';

const KIND: Record<string, string> = {
  chat: 'Chat',
  evolution: 'Evolutions',
  routine: 'Routines',
  health: 'Health (investigating problems)',
  other: 'Other',
};

function linkFor(t: SpendTotal): string | null {
  if (!t.ref) return null;
  switch (t.key) {
    case 'evolution': return `/evolutions/${encodeURIComponent(t.ref)}`;
    case 'routine': return '/routines';
    case 'health': return '/health';
    default: return null;
  }
}

export default function Spending() {
  const load = useLoad(() => api.spending(), []);
  const [data, setData] = useState<SpendingData | null>(null);
  useEffect(() => { if (load.data) setData(load.data); }, [load.data]);
  // Spending changes with every call; the status broadcast is a good moment to refresh.
  const { status } = useLive();
  const month = status?.spend?.month_usd;
  useEffect(() => { if (month != null) load.reload(); }, [month]); // eslint-disable-line react-hooks/exhaustive-deps
  useLiveEvent('message', () => load.reload());

  if (load.error && !data) return <div className="page"><ErrorNote error={load.error} onRetry={load.reload} /></div>;
  if (!data) return <div className="page"><Loading /></div>;
  const m = data.month;
  const monthName = new Date(m.from).toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' });
  const maxKind = Math.max(0.0001, ...m.by_kind.map((k) => k.cost_usd));

  return (
    <div className="page">
      <PageHeader title="Spending" sub="What my thinking costs: every call to my model, as my provider charged it." />

      <section className="panel spend-summary" aria-labelledby="spend-month-h">
        <h2 id="spend-month-h">{monthName}</h2>
        <div className="spend-total">
          <span className="spend-amount">{usd(m.total_usd)}</span>
          {data.budget_usd > 0 && <span className="muted">of {usd(data.budget_usd)}</span>}
          {data.paused && <Badge tone="warn">budget spent</Badge>}
        </div>
        {data.budget_usd > 0 && (
          <div className="spend-meter" role="meter" aria-valuemin={0} aria-valuemax={data.budget_usd} aria-valuenow={m.total_usd}
            aria-label="Spent this month">
            <div className={`spend-meter-fill ${data.paused ? 'spend-over' : ''}`} style={{ width: `${Math.min(100, (m.total_usd / data.budget_usd) * 100)}%` }} />
          </div>
        )}
        <p className="small muted">
          {m.calls} model {m.calls === 1 ? 'call' : 'calls'} this month (UTC).
          {m.unpriced_calls > 0 && ` ${m.unpriced_calls} had no price from my provider and count as $0.`}
        </p>
        <DayChart days={m.by_day} from={m.from} />
      </section>

      <BudgetPanel data={data} onSaved={setData} />

      <section className="panel" aria-labelledby="spend-kind-h">
        <h2 id="spend-kind-h">What it went on</h2>
        {m.by_kind.length === 0 ? <p className="small muted">Nothing yet this month.</p> : (
          <ul className="spend-rows">
            {m.by_kind.map((k) => (
              <li key={k.key}>
                <span className="spend-row-label">{KIND[k.key] ?? k.key}</span>
                <span className="spend-bar"><span style={{ width: `${(k.cost_usd / maxKind) * 100}%` }} /></span>
                <span className="spend-row-num">{usd(k.cost_usd)}</span>
                <span className="spend-row-calls muted small">{k.calls} {k.calls === 1 ? 'call' : 'calls'}</span>
              </li>
            ))}
          </ul>
        )}
        {m.top.length > 0 && (
          <>
            <h3>Most expensive</h3>
            <ul className="spend-top">
              {m.top.map((t) => {
                const to = linkFor(t);
                const name = t.label || t.ref;
                return (
                  <li key={t.key + t.ref}>
                    <span className="muted small spend-top-kind">{(KIND[t.key] ?? t.key).replace(/s? \(.*$|s$/, '')}</span>
                    {to ? <Link to={to} className="spend-top-name">{name}</Link> : <span className="spend-top-name">{name}</span>}
                    <span className="spend-row-num">{usd(t.cost_usd)}</span>
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </section>
    </div>
  );
}

function DayChart({ days, from }: { days: SpendTotal[]; from: string }) {
  const start = new Date(from);
  const today = new Date();
  const count = today.getUTCFullYear() === start.getUTCFullYear() && today.getUTCMonth() === start.getUTCMonth() ? today.getUTCDate() : 31;
  const byDay = new Map(days.map((d) => [d.key, d]));
  const max = Math.max(0.0001, ...days.map((d) => d.cost_usd));
  const cols = Array.from({ length: count }, (_, i) => {
    const d = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth(), i + 1));
    const key = d.toISOString().slice(0, 10);
    return { key, day: i + 1, v: byDay.get(key)?.cost_usd ?? 0 };
  });
  return (
    <div className="spend-days" aria-label="Spending by day">
      {cols.map((c) => (
        <div key={c.key} className="spend-day" title={`${c.key}: ${usd(c.v)}`}>
          <span className="spend-day-bar" style={{ height: `${c.v > 0 ? Math.max(4, (c.v / max) * 100) : 0}%` }} />
          {(c.day === 1 || c.day % 5 === 0) && <span className="spend-day-label">{c.day}</span>}
        </div>
      ))}
    </div>
  );
}

function BudgetPanel({ data, onSaved }: { data: SpendingData; onSaved: (d: SpendingData) => void }) {
  const [value, setValue] = useState(data.budget_usd > 0 ? String(data.budget_usd) : '');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const save = async (v: number) => {
    setBusy(true);
    setErr(null);
    setSaved(null);
    try {
      const d = await api.setBudget(v);
      onSaved(d);
      setValue(v > 0 ? String(d.budget_usd) : '');
      setSaved(v > 0 ? `Budget set to ${usd(d.budget_usd)} a month.` : 'No budget: I never pause on my own.');
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const parsed = Number(value);
  const valid = value.trim() !== '' && Number.isFinite(parsed) && parsed > 0;
  return (
    <section className="panel" aria-labelledby="budget-h">
      <h2 id="budget-h">Monthly budget</h2>
      <p className="small muted">
        Once this month's budget is spent, I stop spending on my own (scheduled routines, investigating and fixing problems)
        until next month, and tell you in chat. Anything you ask me still happens.
      </p>
      <form className="budget-form" onSubmit={(e) => { e.preventDefault(); if (valid) void save(parsed); }}>
        <span className="budget-input">
          <span className="muted">$</span>
          <input className="input" inputMode="decimal" value={value} onChange={(e) => { setValue(e.target.value); setSaved(null); }}
            placeholder="none" aria-label="Monthly budget in US dollars" />
        </span>
        <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !valid}>{busy ? 'Saving…' : 'Save'}</button>
        {data.budget_usd > 0 && (
          <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void save(0)}>Remove budget</button>
        )}
      </form>
      {saved && <div className="small saved-note" role="status">{saved}</div>}
      {err && <div className="error-text small">{err}</div>}
    </section>
  );
}
