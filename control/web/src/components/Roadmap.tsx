import type { Plan } from '../api';

const staged = (plan: Plan | null | undefined): plan is Plan & { stages: NonNullable<Plan['stages']>; stage: number } =>
  !!plan?.stages?.length && typeof plan.stage === 'number' && plan.stage > 0;

/** "Stage 2 of 4" next to a staged plan's title. */
export function StageBadge({ plan }: { plan: Plan | null | undefined }) {
  if (!staged(plan)) return null;
  return <span className="stage-badge">Stage {plan.stage} of {plan.stages.length}</span>;
}

/**
 * The roadmap of a staged goal: earlier stages done, this evolution's stage
 * "now" (or done once it completed), later stages muted. `full` adds the
 * overall goal and each stage's summary.
 */
export function Roadmap({ plan, full, currentDone }: { plan: Plan | null | undefined; full?: boolean; currentDone?: boolean }) {
  if (!staged(plan)) return null;
  const cur = plan.stage;
  return (
    <div className={`roadmap${full ? ' full' : ' compact'}`}>
      {full && plan.goal && <p className="roadmap-goal"><span className="roadmap-goal-label">Your goal</span> {plan.goal}</p>}
      <ol className="roadmap-list">
        {plan.stages.map((s, i) => {
          const n = i + 1;
          const st = n < cur || (n === cur && currentDone) ? 'done' : n === cur ? 'now' : 'later';
          return (
            <li key={i} className={`rm-stage rm-${st}`} aria-current={st === 'now' ? 'step' : undefined}>
              <span className="rm-mark" aria-hidden>{st === 'done' ? '✓' : n}</span>
              <span className="rm-body">
                <span className="rm-title">{s.title}{st === 'now' && <span className="rm-now-tag">now</span>}</span>
                {full && s.summary && <span className="rm-summary">{s.summary}</span>}
              </span>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
