import { NavLink, useParams } from 'react-router-dom';
import { api } from '../api';
import { Markdown } from '../components/Markdown';
import { Empty, ErrorNote, Loading, PageHeader, useLoad } from '../components/ui';

function SkillDetail({ name }: { name: string }) {
  const load = useLoad(() => api.skill(name), [name]);
  if (load.error) return <ErrorNote error={load.error} onRetry={load.reload} />;
  if (!load.data) return <Loading />;
  const s = load.data;
  return (
    <div className="doc-view">
      <div className="doc-head">
        <h2 className="mono">{s.name}</h2>
        <div className="muted small mono">{s.path}</div>
      </div>
      {s.files?.length > 0 && (
        <div className="file-list">
          {s.files.map((f) => <code key={f}>{f}</code>)}
        </div>
      )}
      {s.content ? <Markdown source={s.content} /> : <div className="muted">No SKILL.md content.</div>}
    </div>
  );
}

export default function Skills() {
  const { name } = useParams();
  const load = useLoad(() => api.skills(), []);
  return (
    <div className="page">
      <PageHeader title="Skills" sub="Know-how I have written down for myself." />
      {load.error && <ErrorNote error={load.error} onRetry={load.reload} />}
      {!load.data && load.loading ? <Loading /> : !load.data?.length ? (
        <Empty title="No skills yet">Skills appear as Seed learns from its evolutions.</Empty>
      ) : (
        <div className="split">
          <nav className="split-list">
            {load.data.map((s) => (
              <NavLink key={s.name} to={`/skills/${encodeURIComponent(s.name)}`} className="split-item">
                <div className="mono fg">{s.name}</div>
                <div className="muted small clamp-2">{s.description}</div>
              </NavLink>
            ))}
          </nav>
          <div className="split-detail">
            {name ? <SkillDetail name={name} /> : <div className="muted pad">Select a skill.</div>}
          </div>
        </div>
      )}
    </div>
  );
}
