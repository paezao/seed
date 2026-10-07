import { useMemo } from 'react';

function lineClass(l: string): string {
  if (l.startsWith('diff --git')) return 'd-file';
  if (l.startsWith('+++') || l.startsWith('---') || l.startsWith('index ') || l.startsWith('new file') || l.startsWith('deleted file') || l.startsWith('similarity') || l.startsWith('rename ')) return 'd-meta';
  if (l.startsWith('@@')) return 'd-hunk';
  if (l.startsWith('+')) return 'd-add';
  if (l.startsWith('-')) return 'd-del';
  return 'd-ctx';
}

export function DiffView({ stat, diff }: { stat: string; diff: string }) {
  const lines = useMemo(() => diff.split('\n'), [diff]);
  return (
    <div className="diff">
      {stat.trim() && <pre className="code-block diff-stat">{stat.trimEnd()}</pre>}
      {diff.trim() ? (
        <pre className="diff-body">
          {lines.map((l, i) => (
            <span key={i} className={lineClass(l)}>{l || ' '}{'\n'}</span>
          ))}
        </pre>
      ) : (
        <div className="muted pad-sm">No changes.</div>
      )}
    </div>
  );
}
