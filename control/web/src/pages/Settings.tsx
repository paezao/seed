import { api } from '../api';
import { ErrorNote, Loading, PageHeader, useLoad } from '../components/ui';

function Value({ v }: { v: unknown }) {
  if (v === null || v === undefined) return <span className="v-null">null</span>;
  if (typeof v === 'boolean') return <span className={v ? 'v-true' : 'v-false'}>{String(v)}</span>;
  if (typeof v === 'number') return <span className="v-num">{v}</span>;
  if (typeof v === 'string') return v === '' ? <span className="v-null">""</span> : <span className="v-str">{v}</span>;
  if (Array.isArray(v)) {
    if (v.length === 0) return <span className="v-null">[]</span>;
    if (v.every((x) => x === null || typeof x !== 'object')) {
      return <span className="v-list">{v.map((x, i) => <span key={i} className="chip"><Value v={x} /></span>)}</span>;
    }
    return <Tree obj={Object.fromEntries(v.map((x, i) => [String(i), x]))} />;
  }
  if (typeof v === 'object') {
    return Object.keys(v as object).length ? <Tree obj={v as Record<string, unknown>} /> : <span className="v-null">{'{}'}</span>;
  }
  return <span>{String(v)}</span>;
}

function Tree({ obj }: { obj: Record<string, unknown> }) {
  return (
    <dl className="tree">
      {Object.entries(obj).map(([k, v]) => {
        const nested = v !== null && typeof v === 'object' && !(Array.isArray(v) && v.every((x) => x === null || typeof x !== 'object'));
        return (
          <div key={k} className={`tree-row${nested ? ' nested' : ''}`}>
            <dt>{k}</dt>
            <dd><Value v={v} /></dd>
          </div>
        );
      })}
    </dl>
  );
}

export default function Settings() {
  const load = useLoad(() => api.settings(), []);
  return (
    <div className="page">
      <PageHeader title="Settings" sub={<>Read-only. Settings live in <code>seed.yaml</code>; secrets are redacted.</>} />
      {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading ? <Loading /> : load.data && (
        <section className="panel"><Tree obj={load.data} /></section>
      )}
    </div>
  );
}
