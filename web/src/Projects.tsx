import { useCallback, useState } from 'react';
import { api, type App, type Database, type Project } from './api';
import { NewAppModal } from './Apps';
import { NewDatabaseModal } from './Databases';
import { Badge, Confirm, EmptyState, ErrorAlert, Field, Icon, Modal, useCan, usePoll, useToast } from './ui';

const engineLabel: Record<string, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', redis: 'Redis' };

// One state per service, the way its own page shows it.
function appState(a: App): [string, string] {
  if (a.node_down) return ['offline', 'node offline'];
  const d = a.last_deployment;
  if (d && (d.state === 'queued' || d.state === 'running')) return ['deploying', 'deploying'];
  if (a.running) return a.running.state === 'active' ? ['running', 'running'] : [a.running.state, a.running.state];
  if (d?.state === 'failed') return ['failed', 'deploy failed'];
  return ['stopped', 'not deployed'];
}
function dbState(d: Database): [string, string] {
  if (d.node_down) return ['offline', 'node offline'];
  if (d.state === 'active') return ['running', 'running'];
  if (d.state === 'activating') return ['activating', 'starting'];
  return [d.state || 'stopped', d.state || 'unknown'];
}
const running = (p: Project) =>
  p.apps.filter(a => appState(a)[0] === 'running').length + p.databases.filter(d => dbState(d)[0] === 'running').length;
const total = (p: Project) => p.apps.length + p.databases.length;

/* ---------- list ---------- */
export function ProjectsPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const { can } = useCan();
  const [list, setList] = useState<Project[] | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(() => { api.projects().then(l => { setList(l); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 10000);

  return (
    <>
      <h1 className="sr-only">Projects</h1>
      {error && !list ? (
        <div className="state state-err" role="alert">
          <Icon n="alert" cls="ic-lg" /><h3>Couldn't load projects</h3><p className="muted">{error}</p>
          <button className="btn" onClick={load}><Icon n="refresh" />Retry</button>
        </div>
      ) : !list ? (
        <div className="skel-list" aria-busy="true" aria-label="Loading">
          {[0, 1].map(i => <div key={i} className="skel-row"><i className="skel w30" /><i className="skel w50" /></div>)}
        </div>
      ) : list.length === 1 && total(list[0]) === 0 ? (
        <EmptyState icon="apps" title="No projects yet"
          text="A project groups the services of one product: a frontend, a backend, their database. They share a network on their node and reach each other by name."
          action={can('apps') ? <button className="btn btn-primary" onClick={() => setNewOpen(true)}><Icon n="plus" />New project</button> : undefined} />
      ) : (
        <div className="grid-cards">
          {list.filter(p => p.name !== 'default' || total(p) > 0).map(p => (
            <article key={p.id} className="card app-card" onClick={() => { location.hash = `#/projects/${p.name}`; }}>
              <div className="ac-head">
                <a className="cell-title mono od-truncate" href={`#/projects/${p.name}`} title={p.name}>{p.name}</a>
                <span className="muted od-nowrap" style={{ fontSize: 'var(--fs-xs)' }}>{total(p) ? `${running(p)} of ${total(p)} running` : 'empty'}</span>
              </div>
              <span className="cell-sub od-truncate">{p.description || (p.name === 'default' ? 'Services created without a project.' : 'No description')}</span>
              <div className="od-cluster">
                {p.apps.map(a => <span key={a.id} className="chip"><span className={`dot ${appState(a)[0] === 'running' ? 'ok' : 'idle'}`} aria-hidden="true" /><Icon n="apps" /><span className="mono">{a.name}</span></span>)}
                {p.databases.map(d => <span key={d.id} className="chip"><span className={`dot ${dbState(d)[0] === 'running' ? 'ok' : 'idle'}`} aria-hidden="true" /><Icon n="database" /><span className="mono">{d.name}</span></span>)}
              </div>
            </article>
          ))}
        </div>
      )}
      {newOpen && <NewProjectModal onClose={() => setNewOpen(false)} onCreated={p => { setNewOpen(false); location.hash = `#/projects/${p.name}`; }} />}
    </>
  );
}

function NewProjectModal({ onClose, onCreated }: { onClose: () => void; onCreated: (p: Project) => void }) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    api.createProjectGroup(name, description).then(p => { toast(`Project ${p.name} created`); onCreated(p); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title="New project" onClose={onClose} sub="An empty project. Add an app, a database or several; they get their own network on their node."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !name} onClick={submit}>{busy ? 'Creating…' : 'Create project'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <Field label="Name" hint="DNS label: lowercase letters, digits and '-'.">
          <input className="input mono" value={name} onChange={e => setName(e.target.value)} placeholder="shop" autoFocus />
        </Field>
        <Field label={<>Description <span className="muted">(optional)</span></>}>
          <input className="input" value={description} onChange={e => setDescription(e.target.value)} placeholder="The online store: web, API and its database" />
        </Field>
      </div>
    </Modal>
  );
}

/* ---------- one project ---------- */
export function ProjectDetail({ name }: { name: string }) {
  const toast = useToast();
  const { can } = useCan();
  const [p, setP] = useState<Project | null>(null);
  const [error, setError] = useState('');
  const [picking, setPicking] = useState(false);
  const [creating, setCreating] = useState<'app' | 'database' | null>(null);
  const [removing, setRemoving] = useState(false);
  const load = useCallback(() => { api.project(name).then(x => { setP(x); setError(''); }, e => setError(e.message)); }, [name]);
  usePoll(load, 8000);

  if (error && !p) {
    return <EmptyState icon="alert" title={error === 'project not found' ? 'No such project' : "Couldn't load the project"} text={error}
      action={<a className="btn" href="#/projects">Back to projects</a>} />;
  }
  if (!p) return <div className="skel-list" aria-busy="true" aria-label="Loading"><div className="skel-row"><i className="skel w30" /><i className="skel w50" /></div></div>;

  const project = p.name === 'default' ? '' : p.name;
  const remove = () => api.deleteProject(p.name).then(() => { toast(`${p.name} deleted`); location.hash = '#/projects'; },
    e => { setRemoving(false); toast(e.message, 'bad'); });

  return (
    <>
      <div className="page-head">
        <div className="ph-text">
          <div className="title-row"><h1 className="mono">{p.name}</h1></div>
          <div className="ph-meta"><span>{p.description || `${total(p)} services`}</span></div>
        </div>
        {can('apps') && (
          <div className="ph-actions">
            <button className="btn btn-primary" onClick={() => setPicking(true)}><Icon n="plus" />Create service</button>
            {p.name !== 'default' && (
              <button className="btn-icon" aria-label={`Delete project ${p.name}`} title={total(p) ? 'Delete its services first' : 'Delete project'}
                disabled={total(p) > 0} onClick={() => setRemoving(true)}><Icon n="trash" /></button>
            )}
          </div>
        )}
      </div>

      {total(p) === 0 ? (
        <EmptyState icon="apps" title="Nothing here yet"
          text="Add the pieces of this project: an app from a repo or an image, and the databases it needs. They reach each other by name."
          action={can('apps') ? <button className="btn btn-primary" onClick={() => setPicking(true)}><Icon n="plus" />Create service</button> : undefined} />
      ) : (
        <div className="grid-cards">
          {p.apps.map(a => {
            const [s, l] = appState(a);
            return (
              <article key={a.id} className="card app-card" onClick={() => { location.hash = `#/apps/${a.name}`; }}>
                <div className="ac-head">
                  <span className="od-row" style={{ gap: 'var(--sp2)', minWidth: 0 }}><Icon n="apps" /><a className="cell-title mono od-truncate" href={`#/apps/${a.name}`}>{a.name}</a></span>
                  <Badge s={s} label={l} />
                </div>
                <span className="cell-sub mono od-truncate" title={a.image || a.repo_url}>{a.image || a.repo_url.replace(/^https?:\/\//, '').replace(/\.git$/, '')}</span>
                <div className="od-cluster">
                  <span className="chip mono" title="Its name on the project's network">{a.name}:{a.port}</span>
                  <span className="chip"><Icon n="nodes" /><span className="mono">{a.node_name}</span></span>
                  {a.domains.slice(0, 1).map(d => <span key={d} className="chip"><Icon n="globe" /><span className="mono od-truncate">{d}</span></span>)}
                </div>
              </article>
            );
          })}
          {p.databases.map(d => {
            const [s, l] = dbState(d);
            return (
              <article key={d.id} className="card app-card" onClick={() => { location.hash = `#/databases/${d.name}`; }}>
                <div className="ac-head">
                  <span className="od-row" style={{ gap: 'var(--sp2)', minWidth: 0 }}><Icon n="database" /><a className="cell-title mono od-truncate" href={`#/databases/${d.name}`}>{d.name}</a></span>
                  <Badge s={s} label={l} />
                </div>
                <span className="cell-sub">{engineLabel[d.engine] ?? d.engine} {d.version}</span>
                <div className="od-cluster">
                  <span className="chip mono" title="Its name on the project's network">{d.name}:{d.port}</span>
                  <span className="chip"><Icon n="nodes" /><span className="mono">{d.node_name}</span></span>
                </div>
              </article>
            );
          })}
        </div>
      )}
      {total(p) > 0 && <p className="help">Services here reach each other by name: <span className="mono">http://{p.apps[0]?.name ?? 'api'}:{p.apps[0]?.port ?? 3000}</span>, a database at <span className="mono">{p.databases[0]?.name ?? 'db'}:{p.databases[0]?.port ?? 5432}</span>. Other projects can't reach them.</p>}

      {picking && (
        <Modal title="Create service" sub={`In ${p.name}`} onClose={() => setPicking(false)}>
          <div className="sheet-list rich">
            <button onClick={() => { setPicking(false); setCreating('app'); }}><Icon n="apps" /><span><strong>Application</strong><span className="help">From a Git repo with a Dockerfile, or a published image.</span></span></button>
            <button onClick={() => { setPicking(false); setCreating('database'); }}><Icon n="database" /><span><strong>Database</strong><span className="help">PostgreSQL, MySQL, MariaDB or Redis, with backups.</span></span></button>
            <button disabled><Icon n="deploy" /><span><strong>Compose</strong><span className="help">Coming later.</span></span></button>
          </div>
        </Modal>
      )}
      {creating === 'app' && <NewAppModal project={project} onClose={() => setCreating(null)} onCreated={() => { setCreating(null); load(); }} />}
      {creating === 'database' && <NewDatabaseModal project={project} onClose={() => setCreating(null)} onCreated={() => { setCreating(null); load(); }} />}
      {removing && (
        <Confirm title={`Delete ${p.name}?`} confirmLabel="Delete project" danger onClose={() => setRemoving(false)} onConfirm={remove}>
          <p className="muted">The project is empty; this only removes it from the list.</p>
        </Confirm>
      )}
    </>
  );
}
