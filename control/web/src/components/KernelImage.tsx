import { useEffect, useState } from 'react';
import { imageBlobURL } from '../api';

/** An image served by the kernel (it needs the token header, so it's fetched). */
export function KernelImage({ path, alt, className, onMissing }: {
  path: string;
  alt: string;
  className?: string;
  onMissing?: () => void;
}) {
  const [src, setSrc] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const [big, setBig] = useState(false);
  useEffect(() => {
    let url: string | null = null;
    let cancelled = false;
    setSrc(null);
    setFailed(false);
    imageBlobURL(path)
      .then((u) => { if (cancelled) URL.revokeObjectURL(u); else { url = u; setSrc(u); } })
      .catch(() => { if (!cancelled) { setFailed(true); onMissing?.(); } });
    return () => { cancelled = true; if (url) URL.revokeObjectURL(url); };
  }, [path]); // eslint-disable-line react-hooks/exhaustive-deps
  if (failed) return <div className={`kimg kimg-missing ${className ?? ''}`}>Image unavailable</div>;
  if (!src) return <div className={`kimg kimg-loading ${className ?? ''}`} aria-busy />;
  return (
    <button type="button" className={`kimg ${big ? 'kimg-big' : ''} ${className ?? ''}`} onClick={() => setBig((b) => !b)}
      aria-label={big ? 'Show smaller' : 'Show larger'} aria-pressed={big}>
      <img src={src} alt={alt} />
    </button>
  );
}
