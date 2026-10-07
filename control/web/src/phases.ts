import type { Check, Evolution, EvolutionEvent, EvolutionStatus } from './api';

export type PhaseKey = 'plan' | 'evolve' | 'build' | 'test' | 'launch' | 'health' | 'reflect' | 'apply';
export type PhaseState = 'done' | 'active' | 'failed' | 'waiting' | 'repairing' | 'pending' | 'skipped';
/** A line under a phase. `explain` makes it expandable (click to see why). */
export type PhaseNote = { ok: boolean; text: string; detail?: string; explain?: string[] };
export type PhaseView = { key: PhaseKey; label: string; state: PhaseState; notes: PhaseNote[] };

export const PHASES: { key: PhaseKey; label: string }[] = [
  { key: 'plan', label: 'Planning' },
  { key: 'evolve', label: 'Evolving' },
  { key: 'build', label: 'Build' },
  { key: 'test', label: 'Tests' },
  { key: 'launch', label: 'Launch' },
  { key: 'health', label: 'Health' },
  { key: 'reflect', label: 'Reflect' },
  { key: 'apply', label: 'Apply' },
];

const STATUS_INDEX: Partial<Record<EvolutionStatus, number>> = {
  requested: 0, planning: 0,
  planned: 1, mutating: 1,
  building: 2,
  testing: 3,
  running: 4,
  observing: 5,
  reflecting: 6,
  ready: 7, applying: 7,
  complete: 8,
};

export function checkPhase(name: string): PhaseKey {
  if (name === 'build' || name === 'migrate') return 'build';
  if (name === 'test' || name.startsWith('test')) return 'test';
  if (name === 'launch') return 'launch';
  if (name === 'health' || name.startsWith('http:')) return 'health';
  return 'test';
}

const phaseIdx = (k: PhaseKey) => PHASES.findIndex((p) => p.key === k);

export function checkText(c: Check): string {
  const n = c.name;
  if (c.ok) {
    if (n === 'build') return 'Build successful';
    if (n === 'migrate') return 'Migrations applied';
    if (n === 'test') return c.tests_passed != null ? `${c.tests_passed} test${c.tests_passed === 1 ? '' : 's'} passed` : 'Tests passed';
    if (n === 'launch') return 'Launched';
    if (n === 'health') return 'Healthy';
    if (n.startsWith('http:')) return `${n.slice(5)} ok`;
    return `${n} ok`;
  }
  let base: string;
  if (n === 'build') base = 'Build failed';
  else if (n === 'migrate') base = 'Migrations failed';
  else if (n === 'test') base = c.tests_failed ? `${c.tests_failed} test${c.tests_failed === 1 ? '' : 's'} failed` : 'Tests failed';
  else if (n === 'launch') base = 'Launch failed';
  else if (n === 'health') base = 'Health check failed';
  else if (n.startsWith('http:')) base = `${n.slice(5)} failed`;
  else base = `${n} failed`;
  return `${base} (attempt ${c.attempt})`;
}

/** Deduplicate checks by (name, attempt), keeping the last reported. */
export function dedupeChecks(checks: Check[]): Check[] {
  const map = new Map<string, Check>();
  for (const c of checks ?? []) map.set(`${c.name}#${c.attempt}`, c);
  return [...map.values()].sort((a, b) => a.attempt - b.attempt);
}

/** Furthest phase reached, inferred from checks/reflection, for evolutions that stopped early. */
function reachedIndex(evo: Evolution, checks: Check[]): number {
  let idx = evo.plan ? 1 : 0;
  const maxAttempt = Math.max(0, ...checks.map((c) => c.attempt));
  for (const c of checks) {
    if (c.attempt !== maxAttempt) continue;
    idx = Math.max(idx, phaseIdx(checkPhase(c.name)));
  }
  if (evo.reflection) idx = Math.max(idx, 6);
  if (evo.commit && evo.new_generation) idx = Math.max(idx, 7);
  return idx;
}

export function derivePhases(evo: Evolution): PhaseView[] {
  const checks = dedupeChecks(evo.checks);
  const maxAttempt = Math.max(0, ...checks.map((c) => c.attempt));
  const status = evo.status;
  const known = STATUS_INDEX[status];
  const terminalStop = status === 'failed' || status === 'cancelled' || status === 'rolled_back';
  const cur = known ?? reachedIndex(evo, checks);
  const live = known !== undefined && status !== 'complete';

  return PHASES.map((p, i) => {
    // Keep failures from earlier attempts (they tell the repair story), but only
    // the latest result for a check that later ran again.
    const phaseChecks = checks.filter((c) => checkPhase(c.name) === p.key);
    const notes: PhaseNote[] = phaseChecks
      .filter((c) => !c.ok || !phaseChecks.some((o) => o.name === c.name && o.attempt > c.attempt))
      .map((c) => ({ ok: c.ok, text: checkText(c), detail: c.detail, explain: !c.ok && c.detail ? [c.detail] : undefined }));

    if (p.key === 'plan' && evo.plan && evo.plan.steps?.length) {
      notes.push({ ok: true, text: `Plan ready — ${evo.plan.steps.length} step${evo.plan.steps.length === 1 ? '' : 's'}` });
    }
    if (p.key === 'reflect' && evo.reflection) {
      const r = evo.reflection;
      // Older reflections have no gaps: fall back to the summary and recorded debt.
      const why = r.gaps?.length ? r.gaps : [r.summary, ...(r.debt ?? [])].filter(Boolean);
      notes.push({
        ok: r.goal_satisfied,
        text: r.goal_satisfied ? 'Goal satisfied' : 'Goal not fully satisfied',
        explain: r.goal_satisfied ? undefined : why,
      });
    }
    if (p.key === 'apply' && status === 'complete' && evo.new_generation != null) {
      notes.push({ ok: true, text: `Generation ${evo.new_generation} committed` });
    }

    let state: PhaseState;
    if (status === 'complete') state = 'done';
    else if (i < cur) state = 'done';
    else if (i === cur) {
      if (status === 'failed') state = 'failed';
      else if (status === 'cancelled' || status === 'rolled_back') state = 'skipped';
      else if (status === 'needs_input') state = 'waiting';
      else state = 'active';
    } else state = terminalStop ? 'skipped' : 'pending';

    // A later phase failed on the latest attempt and the builder went back to fix it.
    if (live && i > cur) {
      const failedLatest = checks.find((c) => checkPhase(c.name) === p.key && c.attempt === maxAttempt && !c.ok);
      if (failedLatest) {
        state = 'repairing';
        const n = notes.find((x) => x.text === checkText(failedLatest));
        if (n) n.text += ' — repairing';
      }
    }
    return { key: p.key, label: p.label, state, notes };
  });
}

/**
 * Plan step progress: all done once the evolution moved past mutation. While
 * mutating, an evolution event whose `data.step` is a number (0-based index of
 * the step being worked on) advances the checklist; otherwise the first step
 * is shown as in progress.
 */
export function stepProgress(evo: Evolution, events: EvolutionEvent[]): { done: number; active: number } {
  const steps = evo.plan?.steps?.length ?? 0;
  const idx = STATUS_INDEX[evo.status];
  if (evo.status === 'complete' || (idx !== undefined && idx >= 2)) return { done: steps, active: -1 };
  if (evo.status !== 'mutating') {
    if (idx === undefined) {
      // stopped: infer from checks whether mutation finished
      const reached = reachedIndex(evo, dedupeChecks(evo.checks));
      return reached >= 2 ? { done: steps, active: -1 } : { done: 0, active: -1 };
    }
    return { done: 0, active: -1 };
  }
  let cur = 0;
  for (const e of events) {
    const d = e.data as { step?: unknown } | null;
    if (d && typeof d === 'object' && typeof d.step === 'number') cur = Math.max(cur, d.step);
  }
  cur = Math.min(cur, Math.max(steps - 1, 0));
  return { done: cur, active: cur };
}
