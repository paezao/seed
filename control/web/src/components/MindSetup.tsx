import { api } from '../api';
import { ModelForm } from './ModelForm';
import { ErrorNote, Loading, useLoad } from './ui';

/** First-run card: the Seed asks for a mind (provider, key, model) before anything else. */
export function MindSetup({ compact }: { compact?: boolean }) {
  const load = useLoad(() => api.model(), []);
  return (
    <section className={`mind-setup${compact ? ' compact' : ''}`} aria-labelledby="mind-setup-title">
      <h2 id="mind-setup-title" className="mind-title">First, give me a mind.</h2>
      <p className="mind-line">
        I think with a language model. Choose a provider and a model; your key is stored in your user
        config (<code>~/.config/seed</code>), never in my repository.
      </p>
      {load.error && !load.data && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading && <Loading />}
      {load.data && (
        <ModelForm config={load.data} onSaved={load.setData} submitLabel="Wake up" busyLabel="Waking up…" />
      )}
    </section>
  );
}
