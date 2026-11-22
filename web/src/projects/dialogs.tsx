// A project's dialogs, opened from its page and from its card's menu.
import { api, type Project } from '../api';
import { Confirm, EnvModal, useToast } from '../ui';

// ProjectVariables edits the variables the project's services share (${{shared.KEY}}).
export function ProjectVariables({ p, onClose }: { p: Project; onClose: () => void }) {
  return (
    <EnvModal title={`Variables · ${p.name}`} savedMsg="Shared variables saved: deploy the apps that use them"
      sub="Shared by the project's services. An app uses one by writing ${{shared.KEY}} in its own variables; it's filled in on each deploy."
      load={() => api.projectEnv(p.name)} save={vars => api.setProjectEnv(p.name, vars)}
      onClose={onClose} onSaved={onClose} />
  );
}

// DeleteProject asks before deleting an empty project (the hub refuses one with services).
export function DeleteProject({ p, onClose, onDeleted }: { p: Project; onClose: () => void; onDeleted: () => void }) {
  const toast = useToast();
  const remove = () => api.deleteProject(p.name).then(() => { toast(`${p.name} deleted`); onDeleted(); },
    e => { onClose(); toast(e.message, 'bad'); });
  return (
    <Confirm title={`Delete ${p.name}?`} confirmLabel="Delete project" danger onClose={onClose} onConfirm={remove}>
      <p className="muted">The project is empty; this only removes it from the list.</p>
    </Confirm>
  );
}

// deletable: the default project stays, and a project with services can't go until they do.
export const deletable = (p: Project) => p.name !== 'default' && p.apps.length + p.databases.length === 0;
