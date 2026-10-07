import { useParams } from 'react-router-dom';
import { useLive } from '../live';
import { Empty, Loading } from '../components/ui';

/** The organism's own origin for framed admin screens (see the kernel's OrganismFrameHost). */
function frameURL(path: string): string {
  const port = window.location.port ? `:${window.location.port}` : '';
  return `${window.location.protocol}//organism.localhost${port}${path}`;
}

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
        <a className="small link-quiet" href={frameURL(ext.path)} target="_blank" rel="noreferrer">Open ↗</a>
      </div>
      {/* An admin screen the organism contributes. It is framed from a separate
          origin (organism.localhost), never this one: same-origin, its script
          could reach into this page and take the control token. */}
      <iframe key={ext.id} src={frameURL(ext.path)} title={ext.title}
        sandbox="allow-scripts allow-forms allow-same-origin allow-popups allow-downloads" />
    </div>
  );
}
