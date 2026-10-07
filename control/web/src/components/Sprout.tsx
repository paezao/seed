import { useEffect, useRef, useState, type CSSProperties } from 'react';
import type { Evolution } from '../api';
import { growth, type GrowthStage } from '../phases';

const STAGE_LABEL: Record<GrowthStage, string> = {
  0: 'a seed', 1: 'a sprout', 2: 'growing leaves', 3: 'a bud', 4: 'in bloom',
};

// Tiny leaves thrown out once when a generation completes: [angle deg, distance px, delay s].
const BURST: [number, number, number][] = [
  [-150, 30, 0], [-120, 36, 0.04], [-90, 34, 0.02], [-60, 38, 0.06], [-30, 30, 0.01],
  [-105, 24, 0.1], [-75, 26, 0.12], [-165, 22, 0.08], [-15, 24, 0.09],
];

/**
 * A small self-drawn plant that grows with the evolution: seed → sprout →
 * leaves → bud → bloom. Sways gently while active, glows softly while
 * waiting on the owner, wilts grey when an evolution fails or is cancelled.
 * Fixed size, so state changes never shift layout.
 */
export function Sprout({ evolution, size = 40 }: { evolution: Evolution; size?: number }) {
  const { stage, mood } = growth(evolution);
  const status = evolution.status;

  // Celebrate only a completion we witnessed (not every finished card in history).
  const prev = useRef(status);
  const [celebrate, setCelebrate] = useState(false);
  useEffect(() => {
    const was = prev.current;
    prev.current = status;
    if (status !== 'complete' || was === 'complete') return;
    setCelebrate(true);
    const t = window.setTimeout(() => setCelebrate(false), 1800);
    return () => window.clearTimeout(t);
  }, [status]);

  const cls = [
    'sprout', `sprout-${mood}`,
    stage >= 1 && 'has-sprout', stage >= 2 && 'has-leaves', stage >= 3 && 'has-bud', stage >= 4 && 'has-bloom',
    stage === 0 && 'only-seed', celebrate && 'celebrate',
  ].filter(Boolean).join(' ');

  const label = mood === 'wilted' ? `Stopped as ${STAGE_LABEL[stage]}` : mood === 'waiting' ? 'A seed, waiting for you' : `Growing: ${STAGE_LABEL[stage]}`;

  return (
    <span className={cls} style={{ width: size, height: size }} role="img" aria-label={label} title={label}>
      <svg viewBox="0 0 48 48" width={size} height={size} aria-hidden>
        <circle className="sp-halo" cx="24" cy="38" r="9" />
        <ellipse className="sp-soil" cx="24" cy="43.2" rx="15" ry="2.2" />
        <g className="sp-plant">
          <ellipse className="sp-seed" cx="24" cy="40.6" rx="3.4" ry="2.5" />
          <path className="sp-stem" d="M24 41.5 C 24.6 34, 23.4 26, 24 12" pathLength={1} />
          <path className="sp-part sp-cot-l" d="M24 33 C 20 33.2, 17.4 31.2, 16.4 28 C 20 27.6, 23 29.6, 24 33 Z" />
          <path className="sp-part sp-cot-r" d="M24 33 C 28 33.2, 30.6 31.2, 31.6 28 C 28 27.6, 25 29.6, 24 33 Z" />
          <path className="sp-part sp-leaf-l" d="M23.8 27.5 C 18 28, 13.4 25, 11.8 19.8 C 17.6 19.2, 22.2 22.4, 23.8 27.5 Z" />
          <path className="sp-part sp-leaf-r" d="M24 23 C 29.6 23.2, 34.2 20.2, 35.8 15.4 C 30.2 14.8, 25.6 18, 24 23 Z" />
          <path className="sp-part sp-bud" d="M24 13 C 21.4 11.6, 21 7.8, 24 5 C 27 7.8, 26.6 11.6, 24 13 Z" />
          <g className="sp-part sp-bloom">
            {[0, 72, 144, 216, 288].map((r) => (
              <ellipse key={r} className="sp-petal" cx="24" cy="6.4" rx="2.9" ry="4.2" transform={`rotate(${r} 24 10.8)`} />
            ))}
            <circle className="sp-heart" cx="24" cy="10.8" r="2.3" />
          </g>
        </g>
      </svg>
      {celebrate && (
        <span className="sp-burst" aria-hidden>
          {BURST.map(([a, d, delay], i) => (
            <i key={i} style={{ '--a': `${a}deg`, '--d': `${(d * size) / 48}px`, '--delay': `${delay}s` } as CSSProperties} />
          ))}
        </span>
      )}
    </span>
  );
}
