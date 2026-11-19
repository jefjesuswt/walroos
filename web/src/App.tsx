import { useEffect, useState } from 'react';
import { api, ApiError, type User } from './api';
import { AppDetail, AppsPage, DeploymentsPage } from './Apps';
import { NodesPage } from './Nodes';
import { SettingsPage } from './Settings';
import { WorkspacesPage } from './Workspaces';
import { EmptyState, Icon, Logo } from './ui';

// [route, label, icon, milestone that brings it]
const NAV = [
  ['overview', 'Overview', 'overview', 'M1'],
  ['workspaces', 'Workspaces', 'workspace', ''],
  ['apps', 'Apps', 'apps', ''],
  ['deployments', 'Deployments', 'deploy', ''],
  ['nodes', 'Nodes', 'nodes', ''],
  ['integrations', 'Integrations', 'plug', 'M4'],
  ['settings', 'Settings', 'settings', ''],
] as const;

// ponytail: hash routing, one level deep; add a router when pages get ids and tabs (M1)
const routeParts = () => location.hash.replace(/^#\/?/, '').split('/');
const currentRoute = () => routeParts()[0] || 'nodes';

// Sidebar width: drag the edge (or arrow keys on it). Under SB_SNAP it collapses to the icon rail.
const SB_MIN = 200, SB_MAX = 420, SB_SNAP = 140, SB_DEFAULT = 264;
function setSidebar(x: number): number {
  const collapsed = x < SB_SNAP, w = Math.round(Math.min(SB_MAX, Math.max(SB_MIN, x)));
  document.body.classList.toggle('sb-collapsed', collapsed);
  if (!collapsed) document.body.style.setProperty('--sb', `${w}px`); // not <html>: applyTheme owns its inline style
  try { localStorage.setItem('walroos-sb', String(collapsed ? 0 : w)); } catch { /* won't stick, fine */ }
  return collapsed ? 0 : w;
}
const savedSidebar = () => { try { return Number(localStorage.getItem('walroos-sb') ?? SB_DEFAULT); } catch { return SB_DEFAULT; } };

export function App() {
  const [sb, setSb] = useState(() => setSidebar(savedSidebar()));
  const resize = (x: number) => setSb(setSidebar(x));
  const [route, setRoute] = useState(currentRoute);
  const [me, setMe] = useState<User | null>(null);
  const [authError, setAuthError] = useState('');
  const [drawer, setDrawer] = useState(false);
  const [addNode, setAddNode] = useState(false);
  const [newWorkspace, setNewWorkspace] = useState(false);
  const [newApp, setNewApp] = useState(false);
  const [sub, setSub] = useState(() => decodeURIComponent(routeParts()[1] ?? ''));

  useEffect(() => {
    const on = () => { setRoute(currentRoute()); setSub(decodeURIComponent(routeParts()[1] ?? '')); setDrawer(false); };
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);

  useEffect(() => {
    api.me().then(setMe, (e: ApiError) => setAuthError(e.message));
  }, []);

  useEffect(() => { document.body.classList.toggle('drawer-open', drawer); }, [drawer]);

  // The top bar gets a surface once content scrolls under it, hides while scrolling down and
  // comes back on the way up.
  useEffect(() => {
    let last = window.scrollY;
    const on = () => {
      const y = window.scrollY, b = document.body.classList;
      b.toggle('scrolled', y > 0);
      if (Math.abs(y - last) > 4) b.toggle('tb-hidden', y > last && y > 64);
      last = y;
    };
    window.addEventListener('scroll', on, { passive: true });
    return () => window.removeEventListener('scroll', on);
  }, []);

  const nav = NAV.find(n => n[0] === route);
  useEffect(() => { document.title = `${sub || nav?.[1] || 'Not found'} · walroos`; }, [nav, sub]);

  if (authError) {
    return (
      <div className="content" style={{ paddingTop: '20vh' }}>
        <EmptyState icon="alert" title="You can't use this hub" text={`${authError}. Ask the owner to add your Tailscale login.`} />
      </div>
    );
  }

  return (
    <div className="app">
      <aside className="sidebar" aria-label="Sidebar">
        <div className="sb-top"><a className="brand" href="#/nodes" aria-label="walroos"><Logo /><span className="lbl">walroos</span></a></div>
        <nav className="nav" aria-label="Main">
          {NAV.map(([r, label, icon, soon]) => (
            <a key={r} href={`#/${r}`} aria-current={r === route ? 'page' : undefined} title={soon ? `${label} · coming in ${soon}` : label}>
              <Icon n={icon} /><span className="lbl">{label}</span>
            </a>
          ))}
        </nav>
        {me && (
          <div className="sb-user" title={me.login}>
            <span className="avatar" aria-hidden="true">{me.login[0].toUpperCase()}</span>
            <span className="sb-user-t lbl">
              <span className="od-truncate" style={{ fontWeight: 500 }}>{me.login.split('@')[0]}</span>
              <span className="conn"><span className="dot ok" aria-hidden="true" /><span className="od-truncate mono">{me.login}</span></span>
            </span>
          </div>
        )}
        <div className="sb-resize" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" tabIndex={0}
          aria-valuemin={0} aria-valuemax={SB_MAX} aria-valuenow={sb} title="Drag to resize, double-click to reset"
          onPointerDown={e => { e.preventDefault(); e.currentTarget.setPointerCapture(e.pointerId); }}
          onPointerMove={e => { if (e.currentTarget.hasPointerCapture(e.pointerId)) resize(e.clientX); }}
          onDoubleClick={() => resize(SB_DEFAULT)}
          onKeyDown={e => {
            if (e.key === 'ArrowRight') resize(sb ? sb + 16 : SB_MIN);
            else if (e.key === 'ArrowLeft') resize(sb - 16 < SB_MIN ? 0 : sb - 16);
          }} />
      </aside>
      <div className="scrim" onClick={() => setDrawer(false)} />
      <div className="main">
        <header className="topbar">
          <button className="btn-icon menu-btn" aria-label="Open navigation" onClick={() => setDrawer(true)}><Icon n="menu" /></button>
          <nav className="crumbs" aria-label="Breadcrumb"><ol>
            {sub && nav ? <><li><a href={`#/${route}`}>{nav[1]}</a></li><li><span aria-current="page" className="mono">{sub}</span></li></>
              : <li><span aria-current="page">{nav?.[1] ?? 'Not found'}</span></li>}
          </ol></nav>
          <div className="tb-primary">
            {route === 'nodes' && <button className="btn btn-primary" onClick={() => setAddNode(true)}><Icon n="plus" /><span className="tb-label">Add node</span></button>}
            {route === 'apps' && !sub && <button className="btn btn-primary" onClick={() => setNewApp(true)}><Icon n="plus" /><span className="tb-label">New app</span></button>}
            {route === 'workspaces' && <button className="btn btn-primary" onClick={() => setNewWorkspace(true)}><Icon n="plus" /><span className="tb-label">New workspace</span></button>}
          </div>
        </header>
        <main id="view" tabIndex={-1}>
          <div className="content">
            {route === 'nodes' ? <NodesPage addOpen={addNode} setAddOpen={setAddNode} />
              : route === 'workspaces' ? <WorkspacesPage newOpen={newWorkspace} setNewOpen={setNewWorkspace} />
              : route === 'apps' ? (sub ? <AppDetail key={sub} name={sub} /> : <AppsPage newOpen={newApp} setNewOpen={setNewApp} />)
              : route === 'deployments' ? <DeploymentsPage />
              : route === 'settings' ? <SettingsPage />
              : nav ? <EmptyState icon={nav[2]} title={`${nav[1]} land in ${nav[3]}`} text="See ARCHITECTURE.md for the roadmap." />
              : <EmptyState icon="alert" title="Nothing here" text="That page doesn't exist." action={<a className="btn" href="#/nodes">Back to nodes</a>} />}
          </div>
        </main>
      </div>
    </div>
  );
}
