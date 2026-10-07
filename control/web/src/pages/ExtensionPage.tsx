import { useParams } from 'react-router-dom';
import { useLive } from '../live';
import { Empty, Loading } from '../components/ui';

export default function ExtensionPage() {
  const { id } = useParams();
  const { extensions, status } = useLive();
  const ext = extensions.find((x) => x.id === id);
  if (!ext) return status ? <div className="page"><Empty title="Extension not found" /></div> : <div className="page"><Loading /></div>;
  return (
    <div className="extension-frame">
      <div className="extension-bar">
        <span className="fg">{ext.title}</span>
        <code className="muted small">{ext.path}</code>
        <span className="spacer" />
        <a className="small link-quiet" href={ext.path} target="_blank" rel="noreferrer">Open ↗</a>
      </div>
      {/* An admin screen the organism contributes: an ordinary organism page,
          same origin so it can use its own /api. */}
      <iframe key={ext.id} src={ext.path} title={ext.title} />
    </div>
  );
}
