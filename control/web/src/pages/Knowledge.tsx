import { useSearchParams } from 'react-router-dom';
import { api, type DocRef } from '../api';
import { Logo } from '../components/Logo';
import { Markdown } from '../components/Markdown';
import { useLive } from '../live';
import { ErrorNote, Loading, PageHeader, useLoad } from '../components/ui';

function FileView({ path }: { path: string }) {
  const load = useLoad(() => api.knowledgeFile(path), [path]);
  if (load.error) return <ErrorNote error={load.error} onRetry={load.reload} />;
  if (!load.data) return <Loading />;
  return (
    <div className="doc-view">
      <div className="doc-head"><div className="muted small mono">{load.data.path}</div></div>
      {/\.(md|markdown)$/i.test(load.data.path) || !/\.\w+$/.test(load.data.path)
        ? <Markdown source={load.data.content} />
        : <pre className="code-block">{load.data.content}</pre>}
    </div>
  );
}

type Item = string | { name?: string; description?: string; [k: string]: unknown };

function itemText(x: unknown): { name: string; description?: string } {
  if (typeof x === 'string') return { name: x };
  if (x && typeof x === 'object') {
    const o = x as Record<string, unknown>;
    const name = String(o.name ?? o.title ?? Object.values(o)[0] ?? '');
    const description = o.description != null ? String(o.description) : undefined;
    return { name, description };
  }
  return { name: String(x) };
}

function List({ title, items }: { title: string; items: unknown }) {
  const list = Array.isArray(items) ? (items as Item[]) : [];
  return (
    <div>
      <h3>{title}</h3>
      {list.length === 0 ? <div className="muted small">None yet.</div> : (
        <ul className="self-list">
          {list.map((x, i) => {
            const t = itemText(x);
            return <li key={i}><span className="fg">{t.name}</span>{t.description && <span className="muted"> — {t.description}</span>}</li>;
          })}
        </ul>
      )}
    </div>
  );
}

/** The self model, rendered as "what I believe I am". */
function SelfModel({ model }: { model: Record<string, unknown> }) {
  const { status } = useLive();
  const identity = (model.identity ?? {}) as Record<string, unknown>;
  const purpose = (model.purpose ?? {}) as Record<string, unknown>;
  const arch = (model.architecture ?? {}) as Record<string, unknown>;
  return (
    <div className="self-model">
      <div className="self-head">
        <Logo identity={status?.identity ?? null} size={48} />
        <div>
          <div className="self-name">{String(identity.name ?? '')}</div>
          {identity.tagline ? <div className="muted">{String(identity.tagline)}</div> : null}
          <div className="muted small mono">slug: {String(identity.slug ?? '')}{status?.generation ? ` · generation ${status.generation.number}` : ''}</div>
        </div>
      </div>
      <h3>Purpose</h3>
      <p>{purpose.description ? String(purpose.description) : <span className="muted">I don't have a purpose yet.</span>}</p>
      <div className="self-grid">
        <List title="Capabilities" items={model.capabilities} />
        <div>
          <h3>Architecture</h3>
          <dl className="kv-grid">
            {Object.entries(arch).map(([k, v]) => (<div key={k}><dt>{k}</dt><dd>{String(v)}</dd></div>))}
          </dl>
        </div>
        <List title="Constraints" items={model.constraints} />
        <List title="Goals" items={model.goals} />
      </div>
    </div>
  );
}

function DocList({ title, items, selected, onPick }: { title: string; items: DocRef[]; selected: string | null; onPick: (p: string) => void }) {
  return (
    <div className="doc-group">
      <div className="col-label">{title}</div>
      {items.length === 0 ? <div className="muted small pad-sm">None yet.</div> : items.map((d) => (
        <button key={d.path} className={`split-item${selected === d.path ? ' active' : ''}`} onClick={() => onPick(d.path)}>
          <div className="fg">{d.title || d.path}</div>
          <div className="muted small mono truncate">{d.path}</div>
        </button>
      ))}
    </div>
  );
}

export default function Knowledge() {
  const load = useLoad(() => api.knowledge(), []);
  const [params, setParams] = useSearchParams();
  const selected = params.get('path');
  const pick = (p: string) => setParams({ path: p });

  return (
    <div className="page">
      <PageHeader title="What I am" sub="My self model, my docs, and the decisions that made me this way." />
      {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading ? <Loading /> : load.data && (
        <>
          <section className="panel">
            <h2>Self model</h2>
            {load.data.self_model ? <SelfModel model={load.data.self_model} /> : <div className="muted">No self model yet.</div>}
            {load.data.self && (
              <details className="raw-toggle">
                <summary>knowledge/self.yaml</summary>
                <pre className="code-block">{load.data.self}</pre>
              </details>
            )}
          </section>
          <div className="split">
            <nav className="split-list">
              <DocList title="Docs" items={load.data.docs ?? []} selected={selected} onPick={pick} />
              <DocList title="Decisions" items={load.data.decisions ?? []} selected={selected} onPick={pick} />
            </nav>
            <div className="split-detail">
              {selected ? <FileView path={selected} /> : <div className="muted pad">Select a document.</div>}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
