import { useState } from 'react';
import { api } from '../api';
import { brainProvider, CopyCommand, restartCommand } from './Brain';
import { ModelForm } from './ModelForm';
import { ErrorNote, Loading, useLoad } from './ui';

/** Shown in the chat while the Seed has no key: how to restart it with one, or lend one for this session. */
export function MindSetup({ compact }: { compact?: boolean }) {
  const load = useLoad(() => api.model(), []);
  const [lending, setLending] = useState(false);
  const provider = brainProvider(load.data);
  return (
    <section className={`mind-setup${compact ? ' compact' : ''}`} aria-labelledby="mind-setup-title">
      <h2 id="mind-setup-title" className="mind-title">I need a brain to think.</h2>
      <p className="mind-line">
        No key was passed when I started. Restart me with one{provider?.keys_url && (
          <> (<a className="mf-get-key" href={provider.keys_url} target="_blank" rel="noreferrer">get a key ↗</a>)</>
        )}:
      </p>
      <CopyCommand command={restartCommand(provider)} label="Restart command" />
      {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading && <Loading />}
      {load.data && !lending && (
        <button type="button" className="link-btn mind-lend-toggle" onClick={() => setLending(true)}>
          or lend me a key for this session
        </button>
      )}
      {load.data && lending && (
        <div className="mind-lend-form">
          <ModelForm config={load.data} onSaved={load.setData} submitLabel="Wake up" busyLabel="Waking up…" autoFocusKey
            onCancel={() => setLending(false)} cancelLabel="Never mind" />
        </div>
      )}
    </section>
  );
}
