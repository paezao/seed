import { useEffect, useState } from 'react';
import type { Identity } from '../api';

/** The Seed's current self-drawn logo, or a neutral placeholder. */
export function Logo({ identity, size = 24, className }: { identity: Identity | null; size?: number; className?: string }) {
  const url = identity?.logo_url;
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [url]);
  const style = { width: size, height: size };
  if (!url || failed) return <span className={`logo logo-placeholder ${className ?? ''}`} style={style} aria-hidden />;
  return <img className={`logo ${className ?? ''}`} src={url} alt="" width={size} height={size} style={style} onError={() => setFailed(true)} />;
}
