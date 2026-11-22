import { useCallback, useState } from 'react';
import { api, type Node, type Workspace } from '../api';
import { stackLabel } from '../stack';
import { ActionSheet, ago, Badge, Confirm, EmptyState, EnvModal, Icon, Loading, LoadError, PageBar, RowActions, useCan, useContextMenu, usePoll, useToast, type Action } from '../ui';
import { ConnectModal } from './ConnectModal';
import { NewWorkspaceModal } from './NewWorkspace';

type Dialog = { kind: 'connect' | 'env' | 'rebuild' | 'delete' | 'sheet'; w: Workspace } | null;

// github.com/me/app(.git) → me/app
function repoLabel(url: string): string {
  const m = url.replace(/\.git$/, '').match(/(?:[:/])([^/]+\/[^/]+)$/);
  return m ? m[1] : url;
}

export function WorkspacesPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const toast = useToast();
  const { me, canUse } = useCan();
  const [list, setList] = useState<Workspace[] | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [error, setError] = useState('');
  const [dialog, setDialog] = useState<Dialog>(null);
  const [onMenu, contextMenu] = useContextMenu();

  const load = useCallback(() => {
    api.workspaces().then(w => { setList(w); setError(''); }, e => setError(e.message));
  }, []);
  // creating/running transitions live on the node; poll while anything is settling
  usePoll(load, list?.some(w => w.state === 'creating' || w.state === 'deleting') ? 2000 : 5000);
  usePoll(() => { api.nodes().then(setNodes, () => undefined); }, 10000);

  const op = (w: Workspace, fn: (name: string) => Promise<Workspace>) =>
    fn(w.name).then(next => { toast(`${w.name} ${next.state}`); load(); }, e => toast(e.message, 'bad'));
  const close = () => setDialog(null);

  // Someone else's workspace (and not the hub's owner): connect info only, no actions.
  const busy = (w: Workspace) => w.state === 'creating' || w.state === 'deleting' || !canUse(w.owner);
  const actions = (w: Workspace): Action[] => [
    { icon: 'plug', label: 'Connect', run: () => setDialog({ kind: 'connect', w }) },
    w.state === 'stopped'
      ? { icon: 'deploy', label: 'Start', run: () => op(w, api.startWorkspace), off: busy(w) }
      : { icon: 'x', label: 'Stop', run: () => op(w, api.stopWorkspace), off: busy(w) || w.state !== 'running' },
    { icon: 'settings', label: 'Environment variables', run: () => setDialog({ kind: 'env', w }), off: !canUse(w.owner) },
    { icon: 'refresh', label: 'Rebuild', title: 'Rebuild (keeps the clone and $HOME)', run: () => setDialog({ kind: 'rebuild', w }), off: busy(w) },
    { icon: 'trash', label: 'Delete', run: () => setDialog({ kind: 'delete', w }), off: busy(w), danger: true },
  ];

  return (
    <>
      <h1 className="sr-only">Workspaces</h1>
      {list && list.length > 0 && <PageBar sub={`${list.filter(w => w.state === 'running').length} of ${list.length} running`} />}

      {error && !list ? <LoadError what="workspaces" error={error} onRetry={load} />
        : !list ? <Loading rows={2} cols={[30, 50, 15]} />
        : list.length === 0 ? (
          <EmptyState icon="workspace" title="No workspaces yet"
            text="A workspace is a container on one of your nodes, built from a Git repo. Connect with SSH and everything runs there."
            action={<button className="btn btn-primary" onClick={() => setNewOpen(true)}><Icon n="plus" />New workspace</button>} />
        ) : (
          <div className="table-wrap">
            <table className="tbl">
              <thead><tr>
                <th className="col-grow">Workspace</th><th className="hide-sm">Node</th><th>State</th>
                <th className="hide-lg">Stack</th><th className="hide-sm">Created</th>
                <th className="hide-xs"><span className="sr-only">Actions</span></th><th className="show-xs"><span className="sr-only">More</span></th>
              </tr></thead>
              <tbody>
                {list.map(w => (
                  <tr key={w.id} onContextMenu={onMenu(w.name, actions(w))}>
                    <td className="col-grow"><div className="cell-lead">
                      <span className={`lead${w.state === 'running' ? '' : ' off'}`} aria-hidden="true"><Icon n="workspace" /></span>
                      <div className="cell-stack">
                        <span className="cell-title mono" title={w.name}>{w.name}</span>
                        <span className="cell-sub mono od-truncate" title={w.repo_url}>{w.repo_url ? repoLabel(w.repo_url) : 'local git repo'}{w.branch ? ` · ${w.branch}` : ''}{w.owner !== me?.login ? ` · ${w.owner}` : ''}</span>
                      </div>
                    </div></td>
                    <td className="hide-sm mono"><span className="clip" title={w.node_name}>{w.node_name}</span></td>
                    <td><Badge s={w.state} />
                      {w.state === 'failed' && w.error && <div className="cell-sub od-truncate err-line" title={w.error}>{w.error}</div>}
                    </td>
                    <td className="hide-lg od-nowrap" title={w.image}>{stackLabel(w.stack, w.image) || <span className="muted">—</span>}</td>
                    <td className="hide-sm muted od-nowrap">{ago(w.created_at)}</td>
                    <td className="ta-r od-nowrap hide-xs"><RowActions name={w.name} actions={actions(w)} /></td>
                    <td className="ta-r show-xs">
                      <button className="btn-icon" aria-label={`Actions for ${w.name}`} onClick={() => setDialog({ kind: 'sheet', w })}><Icon n="more" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

      {contextMenu}
      {newOpen && <NewWorkspaceModal nodes={nodes} onClose={() => setNewOpen(false)} onCreated={() => { setNewOpen(false); load(); }} />}
      {dialog && <WorkspaceDialog d={dialog} actions={actions(dialog.w)} canUse={canUse(dialog.w.owner)} onClose={close} onDone={() => { close(); load(); }} />}
    </>
  );
}

function WorkspaceDialog({ d: { kind, w }, actions, canUse, onClose, onDone }: {
  d: NonNullable<Dialog>; actions: Action[]; canUse: boolean; onClose: () => void; onDone: () => void;
}) {
  const toast = useToast();
  switch (kind) {
    case 'sheet':
      return <ActionSheet title={w.name} sub={`${w.node_name} · ${w.state}${canUse ? '' : ` · ${w.owner}'s`}`} actions={actions} onClose={onClose} />;
    case 'connect':
      return <ConnectModal w={w} onClose={onClose} />;
    case 'env':
      return (
        <EnvModal title={`Environment · ${w.name}`} savedMsg="Variables saved: rebuild to apply them"
          sub="Passed to the container as KEY=VALUE. Values are encrypted in the hub; they apply when the container is recreated (Rebuild)."
          load={() => api.workspaceEnv(w.name)} save={vars => api.setWorkspaceEnv(w.name, vars)} onClose={onClose} onSaved={onDone} />
      );
    case 'rebuild':
      return (
        <Confirm title={`Rebuild ${w.name}?`} confirmLabel="Rebuild" onClose={onClose}
          onConfirm={() => { onClose(); api.rebuildWorkspace(w.name).then(() => { toast(`${w.name} is rebuilding`); onDone(); }, e => toast(e.message, 'bad')); }}>
          <p className="muted">The container and any image built from the repo are recreated; a changed <span className="mono">devcontainer.json</span> or a new base image is picked up. The clone and <span className="mono">$HOME</span> (mise runtimes, <span className="mono">~/.claude</span>, caches) stay.</p>
        </Confirm>
      );
    case 'delete':
      return (
        <Confirm title={`Delete ${w.name}?`} confirmLabel="Delete workspace" danger onClose={onClose}
          onConfirm={() => api.deleteWorkspace(w.name).then(() => { toast(`${w.name} deleted`); onDone(); }, e => toast(e.message, 'bad'))}>
          <p className="muted">This removes the container <strong>and</strong> its volumes: the clone and everything in <span className="mono">$HOME</span> (mise runtimes, <span className="mono">~/.claude</span>, caches). It can't be undone.</p>
        </Confirm>
      );
  }
}
