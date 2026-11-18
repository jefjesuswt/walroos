import { useEffect, useState } from 'react';
import { api, ApiError, type User } from './api';
import { NodesPage } from './Nodes';
import { SettingsPage } from './Settings';
import { WorkspacesPage } from './Workspaces';
import { EmptyState, Icon, Logo } from './ui';

// [route, label, icon, milestone that brings it]
const NAV = [
  ['overview', 'Overview', 'overview', 'M1'],
  ['workspaces', 'Workspaces', 'workspace', ''],
  ['apps', 'Apps', 'apps', 'M3'],
  ['deployments', 'Deployments', 'deploy', 'M3'],
  ['nodes', 'Nodes', 'nodes', ''],
  ['integrations', 'Integrations', 'plug', 'M4'],
  ['settings', 'Settings', 'settings', ''],
] as const;

// ponytail: hash routing, one level deep; add a router when pages get ids and tabs (M1)
const currentRoute = () => location.hash.replace(/^#\/?/, '').split('/')[0] || 'nodes';

export function App() {
  const [route, setRoute] = useState(currentRoute);
  const [me, setMe] = useState<User | null>(null);
  const [authError, setAuthError] = useState('');
  const [drawer, setDrawer] = useState(false);
  const [addNode, setAddNode] = useState(false);
  const [newWorkspace, setNewWorkspace] = useState(false);

  useEffect(() => {
    const on = () => { setRoute(currentRoute()); setDrawer(false); };
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);

  useEffect(() => {
    api.me().then(setMe, (e: ApiError) => setAuthError(e.message));
  }, []);

  useEffect(() => { document.body.classList.toggle('drawer-open', drawer); }, [drawer]);

  const nav = NAV.find(n => n[0] === route);
  useEffect(() => { document.title = `${nav?.[1] ?? 'Not found'} · walroos`; }, [nav]);

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
      </aside>
      <div className="scrim" onClick={() => setDrawer(false)} />
      <div className="main">
        <header className="topbar">
          <button className="btn-icon menu-btn" aria-label="Open navigation" onClick={() => setDrawer(true)}><Icon n="menu" /></button>
          <nav className="crumbs" aria-label="Breadcrumb"><ol><li><span aria-current="page">{nav?.[1] ?? 'Not found'}</span></li></ol></nav>
          <div className="tb-primary">
            {route === 'nodes' && <button className="btn btn-primary" onClick={() => setAddNode(true)}><Icon n="plus" /><span className="tb-label">Add node</span></button>}
            {route === 'workspaces' && <button className="btn btn-primary" onClick={() => setNewWorkspace(true)}><Icon n="plus" /><span className="tb-label">New workspace</span></button>}
          </div>
        </header>
        <main id="view" tabIndex={-1}>
          <div className="content">
            {route === 'nodes' ? <NodesPage addOpen={addNode} setAddOpen={setAddNode} />
              : route === 'workspaces' ? <WorkspacesPage newOpen={newWorkspace} setNewOpen={setNewWorkspace} />
              : route === 'settings' ? <SettingsPage />
              : nav ? <EmptyState icon={nav[2]} title={`${nav[1]} land in ${nav[3]}`} text="See ARCHITECTURE.md for the roadmap." />
              : <EmptyState icon="alert" title="Nothing here" text="That page doesn't exist." action={<a className="btn" href="#/nodes">Back to nodes</a>} />}
          </div>
        </main>
      </div>
    </div>
  );
}
