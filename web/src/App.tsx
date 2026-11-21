import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type User } from './api';
import { AppDetail, DeploymentsPage } from './Apps';
import { DatabaseDetail } from './Databases';
import { ProjectDetail, ProjectsPage } from './Projects';
import { NodesPage } from './Nodes';
import { SettingsPage } from './Settings';
import { WorkspacesPage } from './Workspaces';
import { allowed, EmptyState, Icon, Logo, MeCtx, TrailCtx, type Crumb, type Perm } from './ui';

// [route, label, icon, milestone that brings it]. Apps and databases live in projects (their
// pages keep their routes); PARENT says which nav entry their detail pages belong to.
const NEEDS: Record<string, Perm> = {};
const PARENT: Record<string, string> = { apps: 'projects', databases: 'projects' };
const HIDDEN = [['apps', 'Apps', 'apps', ''], ['databases', 'Databases', 'database', '']] as const;
const NAV = [
  ['overview', 'Overview', 'overview', 'M1'],
  ['workspaces', 'Workspaces', 'workspace', ''],
  ['projects', 'Projects', 'apps', ''],
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
  const [newProject, setNewProject] = useState(false);
  const [sub, setSub] = useState(() => decodeURIComponent(routeParts()[1] ?? ''));
  const [trail, setTrail] = useState<Crumb[]>([]); // a detail page's parents (Projects / shop)

  useEffect(() => {
    const on = () => {
      // Apps and databases are listed in their projects now; old links land there.
      if (/^#\/(apps|databases)\/?$/.test(location.hash)) { location.replace('#/projects'); return; }
      setRoute(currentRoute()); setSub(decodeURIComponent(routeParts()[1] ?? '')); setDrawer(false);
    };
    on();
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);

  useEffect(() => {
    api.me().then(setMe, (e: ApiError) => setAuthError(e.message));
  }, []);

  useEffect(() => { document.body.classList.toggle('drawer-open', drawer); }, [drawer]);

  // Material 3 "enter always" top app bar, like Compose's enterAlwaysScrollBehavior: the bar
  // follows the scroll pixel for pixel (scroll down 20px, it slides up 20px; scroll back up, it
  // comes back the same way), and when scrolling stops it settles fully shown or fully hidden,
  // whichever is closer. Away from the top it takes a container color and a hairline (tb-scrolled).
  const topbar = useRef<HTMLElement>(null);
  useEffect(() => {
    const bar = topbar.current;
    if (!bar) return;
    let last = Math.max(0, window.scrollY), offset = 0, settle = 0;
    const move = (v: number, animate: boolean) => {
      offset = v;
      bar.classList.toggle('tb-settling', animate); // the transition only runs for the settle
      bar.style.top = `${-v}px`;
    };
    const on = () => {
      const y = Math.max(0, window.scrollY); // ignore rubber-band overscroll above the top
      const h = bar.offsetHeight + 1;
      bar.classList.toggle('tb-scrolled', y > 0);
      move(y === 0 ? 0 : Math.min(h, Math.max(0, offset + y - last)), false);
      last = y;
      window.clearTimeout(settle);
      settle = window.setTimeout(() => {
        const show = offset < h / 2 || window.scrollY < h; // near the top it always shows
        if (offset !== 0 && offset !== h) move(show ? 0 : h, true);
      }, 120);
    };
    // A new page may be too short to scroll, and then no scroll event would ever bring the bar
    // (and on phones the menu button) back: show it on every route change.
    const reset = () => { last = Math.max(0, window.scrollY); window.clearTimeout(settle); move(0, true); bar.classList.toggle('tb-scrolled', last > 0); };
    window.addEventListener('scroll', on, { passive: true });
    window.addEventListener('hashchange', reset);
    return () => {
      window.removeEventListener('scroll', on);
      window.removeEventListener('hashchange', reset);
      window.clearTimeout(settle);
    };
  }, []);

  const nav = NAV.find(n => n[0] === route) ?? HIDDEN.find(n => n[0] === route);
  useEffect(() => { document.title = `${sub || nav?.[1] || 'Not found'} · walroos`; }, [nav, sub]);

  if (authError) {
    return (
      <div className="content" style={{ paddingTop: '20vh' }}>
        <EmptyState icon="alert" title="You can't use this hub" text={`${authError}. Ask the owner to add your Tailscale login.`} />
      </div>
    );
  }

  const can = (p: Perm) => allowed(me, p);

  return (
    <MeCtx.Provider value={me}><TrailCtx.Provider value={setTrail}>
    <div className="app">
      <aside className="sidebar" aria-label="Sidebar">
        <div className="sb-top"><a className="brand" href="#/nodes" aria-label="walroos"><Logo /><span className="lbl">walroos</span></a></div>
        <nav className="nav" aria-label="Main">
          {NAV.filter(([r]) => !NEEDS[r] || can(NEEDS[r])).map(([r, label, icon, soon]) => (
            <a key={r} href={`#/${r}`} aria-current={r === (PARENT[route] ?? route) ? 'page' : undefined} title={soon ? `${label} · coming in ${soon}` : label}>
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
              <span className="conn">{me.role}</span>
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
        <header className="topbar" ref={topbar}>
          <button className="btn-icon menu-btn" aria-label="Open navigation" onClick={() => setDrawer(true)}><Icon n="menu" /></button>
          <nav className="crumbs" aria-label="Breadcrumb"><ol>
            {sub && trail.length ? <>{trail.map(c => <li key={c.href}><a href={c.href}>{c.label}</a></li>)}<li><span aria-current="page" className="mono">{sub}</span></li></>
              : sub && nav ? <><li><a href={`#/${route}`}>{nav[1]}</a></li><li><span aria-current="page" className="mono">{sub}</span></li></>
              : <li><span aria-current="page">{nav?.[1] ?? 'Not found'}</span></li>}
          </ol></nav>
          <div className="tb-primary">
            {route === 'nodes' && can('nodes') && <button className="btn btn-primary" onClick={() => setAddNode(true)}><Icon n="plus" /><span className="tb-label">Add node</span></button>}
            {route === 'projects' && !sub && can('apps') && <button className="btn btn-primary" onClick={() => setNewProject(true)}><Icon n="plus" /><span className="tb-label">New project</span></button>}
            {route === 'workspaces' && <button className="btn btn-primary" onClick={() => setNewWorkspace(true)}><Icon n="plus" /><span className="tb-label">New workspace</span></button>}
          </div>
        </header>
        <main id="view" tabIndex={-1}>
          <div className="content">
            {route === 'nodes' ? <NodesPage addOpen={addNode} setAddOpen={setAddNode} />
              : route === 'workspaces' ? <WorkspacesPage newOpen={newWorkspace} setNewOpen={setNewWorkspace} />
              : route === 'apps' && sub ? <AppDetail key={sub} name={sub} />
              : route === 'projects' ? (sub ? <ProjectDetail key={sub} name={sub} /> : <ProjectsPage newOpen={newProject} setNewOpen={setNewProject} />)
              : route === 'deployments' ? <DeploymentsPage />
              : route === 'databases' && sub ? <DatabaseDetail key={sub} name={sub} />
              : route === 'settings' ? <SettingsPage />
              : nav ? <EmptyState icon={nav[2]} title={`${nav[1]} land in ${nav[3]}`} text="See ARCHITECTURE.md for the roadmap." />
              : <EmptyState icon="alert" title="Nothing here" text="That page doesn't exist." action={<a className="btn" href="#/nodes">Back to nodes</a>} />}
          </div>
        </main>
      </div>
    </div>
    </TrailCtx.Provider></MeCtx.Provider>
  );
}
