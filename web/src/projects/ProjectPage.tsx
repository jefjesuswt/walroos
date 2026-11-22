import { useCallback, useEffect, useState, type MouseEvent, type ReactNode } from 'react';
import { api, type App, type Database, type Project, type Template } from '../api';
import { DeleteApp, NewAppModal } from '../apps/dialogs';
import { usePower } from '../apps/power';
import { DeleteDatabase, NewDatabaseModal } from '../databases/dialogs';
import { appState, dbLabel, dbState, engineLogo, type State } from '../status';
import { type Action, ago, BrandLogo, EmptyState, Icon, linkActions, Loading, LoadFailed, Mark, PageHeader, PopMenu, Select, useCan, useContextMenu, usePoll, useToast, useTrail } from '../ui';
import { DeleteProject, deletable, ProjectVariables } from './dialogs';
import { total } from './ProjectsPage';
import { TemplateModal } from './TemplateModal';

type Kind = 'all' | 'app' | 'database';
type Creating = null | 'app' | 'database' | 'template';

// ProjectPage is one project: its services as cards, filters, and Create service.
export function ProjectPage({ name }: { name: string }) {
  const toast = useToast();
  const { can } = useCan();
  const [p, setP] = useState<Project | null>(null);
  const [error, setError] = useState('');
  const [menu, setMenu] = useState(false);
  const [creating, setCreating] = useState<Creating>(null);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [removing, setRemoving] = useState(false);
  const [variables, setVariables] = useState(false);
  const [deleting, setDeleting] = useState<{ app?: App; db?: Database } | null>(null);
  const [onMenu, contextMenu] = useContextMenu();
  const [powerActions, powerDialog] = usePower(() => load());
  const [q, setQ] = useState('');
  const [kind, setKind] = useState<Kind>('all');
  useEffect(() => { api.templates().then(setTemplates, () => undefined); }, []);
  const load = useCallback(() => { api.project(name).then(x => { setP(x); setError(''); }, e => setError(e.message)); }, [name]);
  usePoll(load, 8000);
  useTrail([{ label: 'Projects', href: '#/projects' }]);

  if (error && !p) return <LoadFailed what="project" error={error} notFound={error === 'project not found'} back="#/projects" backLabel="Back to projects" />;
  if (!p) return <Loading />;

  const project = p.name === 'default' ? '' : p.name;
  const match = (n: string, d: string) => !q || n.includes(q.toLowerCase()) || d.toLowerCase().includes(q.toLowerCase());
  const apps = kind === 'database' ? [] : p.apps.filter(a => match(a.name, a.description));
  const dbs = kind === 'app' ? [] : p.databases.filter(d => match(d.name, d.description));
  const fromTemplate = (a: App) => a.image ? templates.find(t => t.image === a.image) : undefined;
  const created = (path: string) => (x: { name: string }) => { setCreating(null); location.hash = `#/${path}/${x.name}`; };
  const run = (what: Promise<unknown>, done: string) => what.then(() => { toast(done); load(); }, e => toast(e.message, 'bad'));
  // The right-click menu of a service's card.
  const appActions = (a: App): Action[] => [...linkActions(`#/apps/${a.name}`), ...(!can('apps') ? [] : [
    { icon: 'deploy', label: a.running ? 'Redeploy' : 'Deploy', run: () => run(api.deployApp(a.name), `Deploying ${a.name}`), off: !a.repo_url && !a.image, title: a.repo_url || a.image ? undefined : 'Set its source first' },
    ...powerActions(a),
    { icon: 'trash', label: 'Delete', run: () => setDeleting({ app: a }), danger: true },
  ])];
  const dbActions = (d: Database): Action[] => [...linkActions(`#/databases/${d.name}`), ...(!can('databases') ? [] : [
    { icon: 'upload', label: 'Back up now', run: () => run(api.backupNow(d.name), `Backing up ${d.name}`), off: d.node_down },
    { icon: 'trash', label: 'Delete', run: () => setDeleting({ db: d }), danger: true },
  ])];

  return (
    <>
      <PageHeader title={p.name} mark={<Icon n="apps" />}
        meta={<span>{p.description || (p.name === 'default' ? 'Services created without a project.' : `${total(p)} services`)}</span>}
        actions={can('apps') && <>
          {deletable(p) && (
            <button className="btn-icon" aria-label={`Delete project ${p.name}`} title="Delete project" onClick={() => setRemoving(true)}><Icon n="trash" /></button>
          )}
          <button className="btn" onClick={() => setVariables(true)}><Icon n="settings" />Variables</button>
          <PopMenu label="Create service" open={menu} setOpen={setMenu} items={[
            { icon: 'apps', title: 'Application', text: 'From a Git repository or a Docker image.', onClick: () => setCreating('app') },
            { icon: 'database', title: 'Database', text: 'PostgreSQL, MySQL, MariaDB, MongoDB, Redis or Valkey.', onClick: () => setCreating('database') },
            { icon: 'deploy', title: 'Compose', text: 'Coming later.', disabled: true },
            { icon: 'overview', title: 'Template', text: 'nginx, n8n, Grafana, Gitea… ready to run.', onClick: () => setCreating('template') },
          ]} />
        </>} />

      {total(p) === 0 ? (
        <EmptyState icon="apps" title="Nothing here yet"
          text="Add the pieces of this project: an application from a repo or an image, and the databases it needs. They reach each other by name."
          action={can('apps') ? <button className="btn btn-primary" onClick={() => setMenu(true)}><Icon n="plus" />Create service</button> : undefined} />
      ) : <>
        <div className="toolbar">
          <input className="input" type="search" value={q} onChange={e => setQ(e.target.value)} placeholder="Filter services…" aria-label="Filter services" />
          <Select value={kind} onChange={v => setKind(v as Kind)} label="Type">
            <option value="all">All types</option>
            <option value="app">Applications</option>
            <option value="database">Databases</option>
          </Select>
        </div>
        <div className="svc-grid">
          {apps.map(a => (
            <ServiceCard key={a.id} href={`#/apps/${a.name}`} name={a.name} state={appState(a)} created={a.created_at} onMenu={onMenu(a.name, appActions(a))}
              mark={fromTemplate(a) ? <Mark logo={fromTemplate(a)?.logo} size={18} /> : a.repo_url || a.image ? <BrandLogo name={a.image ? 'docker' : 'git'} size={18} /> : <Icon n="apps" />}
              sub={a.description || (a.repo_url || a.image ? `${a.name}:${a.port}` : 'No source yet')} />
          ))}
          {dbs.map(d => (
            <ServiceCard key={d.id} href={`#/databases/${d.name}`} name={d.name} state={dbState(d)} created={d.created_at} onMenu={onMenu(d.name, dbActions(d))}
              mark={<Mark logo={engineLogo(d.engine)} icon="database" size={18} />} sub={d.description || dbLabel(d)} />
          ))}
        </div>
        {apps.length + dbs.length === 0 && <p className="muted">No service matches.</p>}
        <p className="help">Services here reach each other by name, like <span className="mono">{p.databases[0]?.name ?? 'db'}:{p.databases[0]?.port ?? 5432}</span>. Other projects can't reach them.</p>
      </>}

      {creating === 'app' && <NewAppModal project={project} onClose={() => setCreating(null)} onCreated={created('apps')} />}
      {creating === 'template' && <TemplateModal templates={templates} project={project} onClose={() => setCreating(null)} onCreated={created('apps')} />}
      {creating === 'database' && <NewDatabaseModal project={project} onClose={() => setCreating(null)} onCreated={created('databases')} />}
      {variables && <ProjectVariables p={p} onClose={() => setVariables(false)} />}
      {removing && <DeleteProject p={p} onClose={() => setRemoving(false)} onDeleted={() => { location.hash = '#/projects'; }} />}
      {deleting?.app && <DeleteApp app={deleting.app} onClose={() => setDeleting(null)} onDeleted={() => { setDeleting(null); load(); }} />}
      {deleting?.db && <DeleteDatabase d={deleting.db} onClose={() => setDeleting(null)} onDeleted={() => { setDeleting(null); load(); }} />}
      {contextMenu}
      {powerDialog}
    </>
  );
}

// ServiceCard is one service of the project: a status dot, its name and mark, a line about it.
function ServiceCard({ href, name, state: [s, label], mark, sub, created, onMenu }: {
  href: string; name: string; state: State; mark: ReactNode; sub: string; created: string; onMenu: (e: MouseEvent) => void;
}) {
  return (
    <a className="card svc-card" href={href} onContextMenu={onMenu}>
      <span className="svc-card-h"><span className="svc-name"><span className={`svc-dot ${s}`} title={label} /><strong className="mono od-truncate">{name}</strong></span>
        <span className="svc-mark" aria-hidden="true">{mark}</span></span>
      <span className="cell-sub od-truncate">{sub}</span>
      <span className="cell-sub muted">Created {ago(created)}</span>
    </a>
  );
}
