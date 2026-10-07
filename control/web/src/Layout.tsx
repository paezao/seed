import { useEffect, useRef } from 'react';
import { NavLink, Outlet, Link, useLocation } from 'react-router-dom';
import { shortCommit } from './api';
import { Logo } from './components/Logo';
import { SeedMark } from './components/SeedMark';
import { useApplyIdentity } from './identity';
import { OrganismBadge } from './components/ui';
import { useLive } from './live';

const ICONS: Record<string, string> = {
  chat: 'M3 4.5A1.5 1.5 0 0 1 4.5 3h7A1.5 1.5 0 0 1 13 4.5v5a1.5 1.5 0 0 1-1.5 1.5H7l-3 2.5V11h.5',
  evolutions: 'M3 13c3 0 3-10 5-10s2 10 5 10',
  generations: 'M5 3v10M5 5.5a2 2 0 1 0 0-.01M11 13a2 2 0 1 0 0-.01M5 9c0 2 6 0 6 4',
  skills: 'M8 2.5l1.6 3.3 3.6.5-2.6 2.5.6 3.6L8 10.7l-3.2 1.7.6-3.6-2.6-2.5 3.6-.5z',
  knowledge: 'M3 3.5h4a1.5 1.5 0 0 1 1 .5 1.5 1.5 0 0 1 1-.5h4V12H9a1 1 0 0 0-1 1 1 1 0 0 0-1-1H3z',
  logs: 'M3 4h10M3 7h10M3 10h7M3 13h5',
  settings: 'M3 5h6M11 5h2M3 11h2M7 11h6M9 3.5v3M5 9.5v3',
  ext: 'M3 3h10v10H3zM3 6h10',
};

function Icon({ name }: { name: string }) {
  return (
    <svg className="nav-icon" width="16" height="16" viewBox="0 0 16 16" aria-hidden>
      <path d={ICONS[name] ?? ICONS.ext} fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

const NAV = [
  { to: '/', label: 'Chat', icon: 'chat', end: true },
  { to: '/evolutions', label: 'Evolutions', icon: 'evolutions' },
  { to: '/generations', label: 'Generations', icon: 'generations' },
  { to: '/skills', label: 'Skills', icon: 'skills' },
  { to: '/knowledge', label: 'Knowledge', icon: 'knowledge' },
  { to: '/logs', label: 'Logs', icon: 'logs' },
  { to: '/settings', label: 'Settings', icon: 'settings' },
];

export default function Layout() {
  const { status, reachable, streaming, extensions, approvals, retry } = useLive();
  const gen = status?.generation;
  const active = status?.active_evolution;
  const identity = status?.identity ?? null;
  useApplyIdentity(identity);
  const { pathname } = useLocation();
  const contentRef = useRef<HTMLElement>(null);
  useEffect(() => { contentRef.current?.scrollTo(0, 0); }, [pathname]);

  return (
    <div className="app">
      <aside className="sidebar">
        <Link to="/" className="brand">
          <Logo identity={identity} size={24} className="brand-logo" />
          <span className="brand-text">
            {identity?.name ? <span className="brand-name">{identity.name}</span> : <span className="brand-name skeleton" />}
            {identity?.tagline && <span className="brand-tagline">{identity.tagline}</span>}
          </span>
        </Link>
        <nav className="nav">
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.end} className="nav-item">
              <Icon name={n.icon} />
              <span>{n.label}</span>
              {n.to === '/' && approvals.length > 0 && <span className="nav-count">{approvals.length}</span>}
            </NavLink>
          ))}
          {extensions.length > 0 && <div className="nav-section">Admin</div>}
          {extensions.map((x) => (
            <NavLink key={x.id} to={`/x/${encodeURIComponent(x.id)}`} className="nav-item">
              <Icon name="ext" />
              <span>{x.title}</span>
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">
          {status && (
            <Link
              to="/settings"
              className="model-line model-link"
              title={status.model.configured ? 'My mind — change it in Settings' : 'No mind yet — choose a model in Settings'}
            >
              <span className={`dot ${status.model.configured ? 'dot-ok' : 'dot-warn'}`} />
              <span className="mono truncate">{status.model.configured ? `${status.model.provider}/${status.model.name}` : 'no mind yet'}</span>
            </Link>
          )}
          <div className="model-line">
            <span className={`dot ${streaming ? 'dot-ok' : 'dot-off'}`} />
            <span className="mono">{streaming ? 'live' : 'connecting…'}</span>
          </div>
          <div className="kernel-line" title="The seed kernel that hosts and evolves me">
            <SeedMark size={11} />
            <span className="mono truncate">kernel{gen ? ` · generation ${gen.number}` : ''}</span>
          </div>
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div className="topbar-left">
            {identity?.name ? (
              <span className="topbar-name">
                {identity.name}
                {identity.tagline && <span className="topbar-tagline">{identity.tagline}</span>}
              </span>
            ) : <span className="topbar-name skeleton" />}
            {gen ? (
              <span className="gen-chip mono" title={gen.title}>
                gen {gen.number}<span className="sep">·</span>{shortCommit(gen.commit)}
              </span>
            ) : status ? <span className="gen-chip mono muted">gen —</span> : null}
            {status && (
              <span title={status.organism.error || undefined}>
                <OrganismBadge state={status.organism.state} />
              </span>
            )}
            {active && (
              <Link to={`/evolutions/${active.id}`} className="active-evo">
                <span className="spinner" /> <span className="truncate">{active.title || active.intent}</span>
                <span className="muted mono">{active.status}</span>
              </Link>
            )}
          </div>
          <div className="topbar-right">
            {status?.purpose && <span className="purpose truncate" title={status.purpose}>{status.purpose}</span>}
            <a className="btn btn-sm" href="/" title="What I currently am">View me at <span className="mono">/</span> ↗</a>
          </div>
        </header>
        {!reachable && (
          <div className="banner banner-bad" role="alert">
            <span className="dot dot-bad" />
            <span><strong>Kernel unreachable.</strong> <span className="muted">Retrying automatically…</span></span>
            <span className="spacer" />
            <button className="btn btn-sm" onClick={retry}>Retry now</button>
          </div>
        )}
        {reachable && status?.organism.state === 'failed' && status.organism.error && (
          <div className="banner banner-warn">
            <span className="dot dot-bad" />
            <span><strong>Organism failed:</strong> <span className="mono small">{status.organism.error}</span></span>
            <span className="spacer" />
            <Link className="btn btn-sm" to="/logs">View logs</Link>
          </div>
        )}
        <main className="content" ref={contentRef}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}
