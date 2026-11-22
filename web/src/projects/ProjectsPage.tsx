import { useCallback, useState, type MouseEvent } from 'react';
import { api, type Project } from '../api';
import { appState, dbState } from '../status';
import { type Action, EmptyState, ErrorAlert, Field, Foot, Icon, linkActions, Loading, LoadError, Modal, useCan, useContextMenu, usePoll, useToast } from '../ui';
import { DeleteProject, deletable, ProjectVariables } from './dialogs';

export const total = (p: Project) => p.apps.length + p.databases.length;
const running = (p: Project) =>
  p.apps.filter(a => appState(a)[0] === 'running').length + p.databases.filter(d => dbState(d)[0] === 'running').length;

// ProjectsPage lists the projects as cards with their services.
export function ProjectsPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const { can } = useCan();
  const [list, setList] = useState<Project[] | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(() => { api.projects().then(l => { setList(l); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 10000);
  const [dialog, setDialog] = useState<{ kind: 'variables' | 'delete'; p: Project } | null>(null);
  const [onMenu, contextMenu] = useContextMenu();
  const actions = (p: Project): Action[] => [...linkActions(`#/projects/${p.name}`), ...(!can('apps') ? [] : [
    { icon: 'settings', label: 'Variables', run: () => setDialog({ kind: 'variables', p }) },
    { icon: 'trash', label: 'Delete project', run: () => setDialog({ kind: 'delete', p }), danger: true, off: !deletable(p),
      title: deletable(p) ? undefined : p.name === 'default' ? 'The default project stays' : 'Delete its services first' },
  ])];

  return (
    <>
      <h1 className="sr-only">Projects</h1>
      {error && !list ? <LoadError what="projects" error={error} onRetry={load} />
        : !list ? <Loading rows={2} />
        : list.length === 1 && total(list[0]) === 0 ? (
          <EmptyState icon="apps" title="No projects yet"
            text="A project groups the services of one product: a frontend, a backend, their database. They share a network on their node and reach each other by name."
            action={can('apps') ? <button className="btn btn-primary" onClick={() => setNewOpen(true)}><Icon n="plus" />New project</button> : undefined} />
        ) : (
          <div className="grid-cards">
            {list.filter(p => p.name !== 'default' || total(p) > 0).map(p => <ProjectCard key={p.id} p={p} onMenu={onMenu(p.name, actions(p))} />)}
          </div>
        )}
      {dialog?.kind === 'variables' && <ProjectVariables p={dialog.p} onClose={() => setDialog(null)} />}
      {dialog?.kind === 'delete' && <DeleteProject p={dialog.p} onClose={() => setDialog(null)} onDeleted={() => { setDialog(null); load(); }} />}
      {contextMenu}
      {newOpen && <NewProjectModal onClose={() => setNewOpen(false)} onCreated={p => { setNewOpen(false); location.hash = `#/projects/${p.name}`; }} />}
    </>
  );
}

function ProjectCard({ p, onMenu }: { p: Project; onMenu: (e: MouseEvent) => void }) {
  const chip = (key: string, icon: string, name: string, up: boolean) => (
    <span key={key} className="chip"><span className={`dot ${up ? 'ok' : 'idle'}`} aria-hidden="true" /><Icon n={icon} /><span className="mono">{name}</span></span>
  );
  return (
    <article className="card app-card" onClick={() => { location.hash = `#/projects/${p.name}`; }} onContextMenu={onMenu}>
      <div className="ac-head">
        <a className="cell-title mono od-truncate" href={`#/projects/${p.name}`} title={p.name}>{p.name}</a>
        <span className="muted od-nowrap ac-count">{total(p) ? `${running(p)} of ${total(p)} running` : 'empty'}</span>
      </div>
      <span className="cell-sub od-truncate">{p.description || (p.name === 'default' ? 'Services created without a project.' : 'No description')}</span>
      <div className="od-cluster">
        {p.apps.map(a => chip(a.id, 'apps', a.name, appState(a)[0] === 'running'))}
        {p.databases.map(d => chip(d.id, 'database', d.name, dbState(d)[0] === 'running'))}
      </div>
    </article>
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
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!name} label="Create project" busyLabel="Creating…" />}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <Field label="Name" hint="DNS label: lowercase letters, digits and '-'.">
          <input className="input mono" value={name} onChange={e => setName(e.target.value)} placeholder="shop" autoFocus />
        </Field>
        <Field label="Description" optional>
          <input className="input" value={description} onChange={e => setDescription(e.target.value)} placeholder="The online store: web, API and its database" />
        </Field>
      </div>
    </Modal>
  );
}
