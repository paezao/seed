import { useEffect, useRef, useState } from 'react';
import { NavLink, Outlet, Link, useLocation, useNavigate } from 'react-router-dom';
import Welcome from './pages/Welcome';
import { api, shortCommit, type Evolution, evolutionTitle, UI_VERSION } from './api';
import { Logo } from './components/Logo';
import { SeedMark } from './components/SeedMark';
import { useApplyIdentity } from './identity';
import { KernelBanner } from './components/KernelUpdate';
import { OrganismBadge } from './components/ui';
import { useLive } from './live';

const ICONS: Record<string, string> = {
  chat: 'M3 4.5A1.5 1.5 0 0 1 4.5 3h7A1.5 1.5 0 0 1 13 4.5v5a1.5 1.5 0 0 1-1.5 1.5H7l-3 2.5V11h.5',
  evolutions: 'M3 13c3 0 3-10 5-10s2 10 5 10',
  generations: 'M5 3v10M5 5.5a2 2 0 1 0 0-.01M11 13a2 2 0 1 0 0-.01M5 9c0 2 6 0 6 4',
  skills: 'M8 2.5l1.6 3.3 3.6.5-2.6 2.5.6 3.6L8 10.7l-3.2 1.7.6-3.6-2.6-2.5 3.6-.5z',
  knowledge: 'M3 3.5h4a1.5 1.5 0 0 1 1 .5 1.5 1.5 0 0 1 1-.5h4V12H9a1 1 0 0 0-1 1 1 1 0 0 0-1-1H3z',
  logs: 'M3 4h10M3 7h10M3 10h7M3 13h5',
  routines: 'M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 1 0 0-11M8 5v3l2 1.5',
  health: 'M2 8.5h2.5l1.5-3 2.5 6 1.5-3H14',
  backups: 'M3 4.5c0-1.1 2.2-2 5-2s5 .9 5 2-2.2 2-5 2-5-.9-5-2zM3 4.5v7c0 1.1 2.2 2 5 2s5-.9 5-2v-7M3 8c0 1.1 2.2 2 5 2s5-.9 5-2',
  spending: 'M8 2.5a5.5 5.5 0 1 0 0 11a5.5 5.5 0 1 0 0-11M9.8 6.2c-.3-.6-1-.9-1.8-.9-1 0-1.8.5-1.8 1.3 0 1.8 3.7.9 3.7 2.7 0 .8-.8 1.4-1.9 1.4-.9 0-1.6-.4-1.9-1M8 4.3v1M8 10.7v1',
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
  { to: '/backups', label: 'Backups', icon: 'backups' },
  { to: '/routines', label: 'Routines', icon: 'routines' },
  { to: '/health', label: 'Health', icon: 'health' },
  { to: '/spending', label: 'Spending', icon: 'spending' },
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
  const navigate = useNavigate();
  const contentRef = useRef<HTMLElement>(null);
  useEffect(() => { contentRef.current?.scrollTo(0, 0); }, [pathname]);

  // First run is a fullscreen experience: until the Seed can think and its
  // owner has said what it should become, there is no control plane chrome.
  // Once shown, it stays until it hands over (onDone), so live status changes
  // (e.g. a purpose being set mid-evolution) never yank it away.
  const [welcomeDone, setWelcomeDone] = useState(false);
  const [hasMessages, setHasMessages] = useState<boolean | null>(null);
  useEffect(() => {
    if (!status || status.purpose) return;
    api.messages(1).then((m) => setHasMessages(m.length > 0)).catch(() => setHasMessages(true));
  }, [status?.purpose, status == null]); // eslint-disable-line react-hooks/exhaustive-deps
  const fresh = !!status && !status.purpose && hasMessages === false;
  const wantsWelcome = !!status && (!status.model.configured || fresh);
  const [welcomeLatched, setWelcomeLatched] = useState(false);
  useEffect(() => { if (wantsWelcome && !welcomeDone) setWelcomeLatched(true); }, [wantsWelcome, welcomeDone]);
  if (!welcomeDone && (wantsWelcome || welcomeLatched)) {
    return <Welcome fresh={!!status && !status.purpose && hasMessages !== true} onDone={() => { setWelcomeDone(true); navigate('/'); }} />;
  }
  // Still finding out whether this is a first run: a calm blank screen instead of a flash of chrome.
  if (!welcomeDone && status && !status.purpose && hasMessages === null) {
    return <div className="welcome" aria-busy="true" />;
  }

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
              {n.to === '/health' && (status?.open_incidents ?? 0) > 0 && <span className="nav-count">{status?.open_incidents}</span>}
              {n.to === '/spending' && status?.spend?.paused && <span className="nav-dot" title="This month's budget is spent" />}
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
              title={status.model.configured ? 'My mind — change it in Settings' : 'No brain: I was started without a key'}
            >
              <span className={`dot ${status.model.configured ? 'dot-ok' : 'dot-warn'}`} />
              <span className="mono truncate">{status.model.configured ? `${status.model.provider}/${status.model.name}` : 'no brain (no key)'}</span>
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
            <ActiveEvolutionPill evolution={active ?? null} />
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
        {reachable && UI_VERSION && status?.ui_version && status.ui_version !== UI_VERSION && (
          <div className="banner banner-info" role="status">
            <span className="dot dot-ok" />
            <span>A newer version of my control plane is ready.</span>
            <span className="spacer" />
            <button className="btn btn-sm btn-primary" onClick={() => window.location.reload()}>Reload</button>
          </div>
        )}
        {reachable && <KernelBanner />}
        <main className="content" ref={contentRef}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}

/**
 * The evolution in progress, in the header. A spinner while it works; when it
 * ends, a check (or cross), then it fades away.
 */
function ActiveEvolutionPill({ evolution }: { evolution: Evolution | null }) {
  const { evolutions } = useLive();
  const [shown, setShown] = useState<Evolution | null>(evolution);
  const [leaving, setLeaving] = useState(false);
  // Follow the live copy (it may finish before status stops reporting it).
  const live = shown ? (evolutions[shown.id] ?? shown) : null;
  const done = live ? ['complete', 'failed', 'cancelled', 'rolled_back'].includes(live.status) : false;

  useEffect(() => {
    if (evolution) { setShown(evolution); setLeaving(false); }
  }, [evolution?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!done) return;
    const fade = window.setTimeout(() => setLeaving(true), 4000);
    const hide = window.setTimeout(() => { setShown(null); setLeaving(false); }, 4600);
    return () => { window.clearTimeout(fade); window.clearTimeout(hide); };
  }, [done, live?.id]);

  if (!live) return null;
  const ok = live.status === 'complete';
  return (
    <Link to={`/evolutions/${live.id}`} className={`active-evo${done ? (ok ? ' is-done' : ' is-failed') : ''}${leaving ? ' is-leaving' : ''}`}>
      {done ? <span className="active-evo-mark" aria-hidden>{ok ? '✓' : '✗'}</span> : <span className="spinner" />}
      <span className="truncate">{evolutionTitle(live)}</span>
      <span className="muted mono">{ok && live.new_generation != null ? `generation ${live.new_generation}` : live.status.replace('_', ' ')}</span>
    </Link>
  );
}
