import { useEffect, useRef, useState } from 'react';
import type { ModelConfig, ProviderInfo } from '../api';

/** The provider whose key the Seed needs: the selected one, else the first. */
export function brainProvider(config: ModelConfig | null | undefined): ProviderInfo | undefined {
  if (!config) return undefined;
  return config.providers.find((p) => p.id === config.provider) ?? config.providers[0];
}

/** The environment variable that carries the key (falls back to OpenRouter's, the v0.1 provider). */
export function keyEnvOf(provider: ProviderInfo | undefined): string {
  return provider?.key_env || 'OPENROUTER_API_KEY';
}

/** "passed at start via $KEY" / "lent for this session" / "" for a provider's key. */
export function keySourceText(p: ProviderInfo): string {
  if (p.key_source === 'env') return `passed at start via $${keyEnvOf(p)}`;
  if (p.key_source === 'session') return 'lent for this session';
  return p.has_key ? 'key available' : '';
}

/** A copyable shell command, e.g. `seed run -e OPENROUTER_API_KEY`. */
export function CopyCommand({ command, label = 'Command' }: { command: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  const codeRef = useRef<HTMLElement>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
    } catch {
      // No clipboard access (e.g. plain http): select the text so the owner can copy it.
      const el = codeRef.current;
      const sel = window.getSelection();
      if (el && sel) { const r = document.createRange(); r.selectNodeContents(el); sel.removeAllRanges(); sel.addRange(r); }
      return;
    }
    setCopied(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <div className="cmd" role="group" aria-label={label}>
      <span className="cmd-prompt" aria-hidden>$</span>
      <code ref={codeRef} className="cmd-text">{command}</code>
      <button type="button" className={`cmd-copy${copied ? ' copied' : ''}`} onClick={() => void copy()} aria-label={copied ? 'Copied' : `Copy: ${command}`}>
        {copied ? 'Copied ✓' : 'Copy'}
      </button>
      <span className="sr-only" aria-live="polite">{copied ? 'Copied to the clipboard' : ''}</span>
    </div>
  );
}

/** The command that restarts a Seed with its key passed from the environment. */
export function restartCommand(provider: ProviderInfo | undefined): string {
  return `seed run -e ${keyEnvOf(provider)}`;
}
