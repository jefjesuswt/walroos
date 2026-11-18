import { useCallback, useState } from 'react';
import { api, type Node, type Workspace } from './api';
import { ago, Badge, Cmd, CopyBtn, EmptyState, Icon, Modal, usePoll, useToast } from './ui';

export function WorkspacesPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const toast = useToast();
  const [list, setList] = useState<Workspace[] | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [error, setError] = useState('');
  const [connecting, setConnecting] = useState<Workspace | null>(null);
  const [removing, setRemoving] = useState<Workspace | null>(null);

  const load = useCallback(() => {
    api.workspaces().then(w => { setList(w); setError(''); }, e => setError(e.message));
  }, []);
  // creating/running transitions live on the node; poll while anything is settling
  usePoll(load, list?.some(w => w.state === 'creating' || w.state === 'deleting') ? 2000 : 5000);
  usePoll(() => { api.nodes().then(setNodes, () => undefined); }, 10000);

  const op = (w: Workspace, fn: (name: string) => Promise<Workspace>) =>
    fn(w.name).then(
      next => { toast(`${w.name} ${next.state}`); load(); },
      e => toast(e.message, 'bad'),
    );

  const remove = (w: Workspace) =>
    api.deleteWorkspace(w.name).then(
      () => { toast(`${w.name} deleted`); setRemoving(null); load(); },
      e => toast(e.message, 'bad'),
    );

  const busy = (w: Workspace) => w.state === 'creating' || w.state === 'deleting';

  return (
    <>
      <h1 className="sr-only">Workspaces</h1>
      {list && list.length > 0 && (
        <div className="page-head page-bar">
          <p className="muted page-sub">{list.filter(w => w.state === 'running').length} of {list.length} running</p><span />
        </div>
      )}

      {error && !list ? (
        <div className="state state-err" role="alert">
          <Icon n="alert" cls="ic-lg" /><h3>Couldn't load workspaces</h3>
          <p className="muted">{error}</p>
          <button className="btn" onClick={load}><Icon n="refresh" />Retry</button>
        </div>
      ) : !list ? (
        <div className="skel-list" aria-busy="true" aria-label="Loading">
          {[0, 1].map(i => <div key={i} className="skel-row"><i className="skel w30" /><i className="skel w50" /><i className="skel w15" /></div>)}
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon="workspace" title="No workspaces yet"
          text="A workspace is a container on one of your nodes, built from a Git repo. Connect with SSH and everything runs there."
          action={<button className="btn btn-primary" onClick={() => setNewOpen(true)}><Icon n="plus" />New workspace</button>} />
      ) : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr>
              <th>Workspace</th><th className="hide-sm">Node</th><th>State</th>
              <th className="hide-md">Image</th><th className="hide-sm">Created</th>
              <th><span className="sr-only">Actions</span></th>
            </tr></thead>
            <tbody>
              {list.map(w => (
                <tr key={w.id}>
                  <td><div className="cell-stack">
                    <span className="cell-title mono od-nowrap">{w.name}</span>
                    <span className="cell-sub mono od-truncate" title={w.repo_url}>{repoLabel(w.repo_url)}{w.branch ? ` · ${w.branch}` : ''}</span>
                  </div></td>
                  <td className="hide-sm mono">{w.node_name}</td>
                  <td><Badge s={w.state} />
                    {w.state === 'failed' && w.error && (
                      <div className="cell-sub od-truncate" style={{ maxWidth: 220, color: 'var(--bad)' }} title={w.error}>{w.error}</div>
                    )}
                  </td>
                  <td className="hide-md mono od-truncate" style={{ maxWidth: 220 }} title={w.image}>{w.image || <span className="muted">—</span>}</td>
                  <td className="hide-sm muted od-nowrap">{ago(w.created_at)}</td>
                  <td className="ta-r od-nowrap">
                    <button className="btn-icon" title="Connect" aria-label={`Connect to ${w.name}`} onClick={() => setConnecting(w)}><Icon n="plug" /></button>
                    {w.state === 'stopped'
                      ? <button className="btn-icon" title="Start" aria-label={`Start ${w.name}`} disabled={busy(w)} onClick={() => op(w, api.startWorkspace)}><Icon n="deploy" /></button>
                      : <button className="btn-icon" title="Stop" aria-label={`Stop ${w.name}`} disabled={busy(w) || w.state !== 'running'} onClick={() => op(w, api.stopWorkspace)}><Icon n="x" /></button>}
                    <button className="btn-icon" title="Delete" aria-label={`Delete ${w.name}`} disabled={busy(w)} onClick={() => setRemoving(w)}><Icon n="trash" /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {newOpen && <NewWorkspaceModal nodes={nodes} onClose={() => setNewOpen(false)} onCreated={() => { setNewOpen(false); load(); }} />}
      {connecting && <ConnectModal w={connecting} onClose={() => setConnecting(null)} />}
      {removing && (
        <Modal title={`Delete ${removing.name}?`} onClose={() => setRemoving(null)}
          foot={<><button className="btn" onClick={() => setRemoving(null)}>Cancel</button>
            <button className="btn btn-danger" autoFocus onClick={() => remove(removing)}>Delete workspace</button></>}>
          <p className="muted">This removes the container <strong>and</strong> its volumes: the clone and everything in <span className="mono">$HOME</span> (mise runtimes, <span className="mono">~/.claude</span>, caches). It can't be undone.</p>
        </Modal>
      )}
    </>
  );
}

// github.com/me/app(.git) → me/app
function repoLabel(url: string): string {
  const m = url.replace(/\.git$/, '').match(/(?:[:/])([^/]+\/[^/]+)$/);
  return m ? m[1] : url;
}

function NewWorkspaceModal({ nodes, onClose, onCreated }: { nodes: Node[]; onClose: () => void; onCreated: () => void }) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [repo, setRepo] = useState('');
  const [branch, setBranch] = useState('');
  const [dcPath, setDcPath] = useState('');
  const [node, setNode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const dev = nodes.filter(n => n.roles.includes('dev') && n.status === 'online');

  const submit = () => {
    setBusy(true);
    api.createWorkspace({ name, repo_url: repo, branch, devcontainer_path: dcPath, node })
      .then(w => { toast(`${w.name} is being created`); onCreated(); },
        e => { setError(e.message); setBusy(false); });
  };

  return (
    <Modal title="New workspace" wide onClose={onClose}
      sub="Cloned from a Git repo and run on a node with the dev role. A devcontainer.json is used when present, else the walroos base image."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !name || !repo} onClick={submit}>{busy ? 'Creating…' : 'Create workspace'}</button>
      </>}>
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't create it</strong><span className="muted">{error}</span></div></div>}
      <div className="form">
        <label className="field"><span className="label">Name</span>
          <input className="input mono" value={name} onChange={e => setName(e.target.value)} placeholder="myproject" autoFocus />
          <span className="help">DNS label. It's also the SSH user: <span className="mono">{name || 'myproject'}.walroos</span>.</span>
        </label>
        <label className="field"><span className="label">Repository</span>
          <input className="input mono" value={repo} onChange={e => setRepo(e.target.value)} placeholder="git@github.com:me/app.git" />
          <span className="help">The node clones with its own git: public repos, or ones its SSH agent can read.</span>
        </label>
        <div className="field-row">
          <label className="field"><span className="label">Branch <span className="muted">(optional)</span></span>
            <input className="input mono" value={branch} onChange={e => setBranch(e.target.value)} placeholder="default branch" />
          </label>
          <label className="field"><span className="label">Node</span>
            <select className="input" value={node} onChange={e => setNode(e.target.value)}>
              <option value="">Auto (least loaded)</option>
              {dev.map(n => <option key={n.id} value={n.name}>{n.name}</option>)}
            </select>
            {dev.length === 0 && <span className="help" style={{ color: 'var(--bad)' }}>No online node with the dev role.</span>}
          </label>
        </div>
        <label className="field"><span className="label">devcontainer.json <span className="muted">(optional)</span></span>
          <input className="input mono" value={dcPath} onChange={e => setDcPath(e.target.value)} placeholder=".devcontainer/devcontainer.json" />
          <span className="help">Leave empty to use the standard location, or the base image if the repo has none.</span>
        </label>
      </div>
    </Modal>
  );
}

function ConnectModal({ w, onClose }: { w: Workspace; onClose: () => void }) {
  const alias = `${w.name}.walroos`;
  return (
    <Modal title={`Connect to ${w.name}`} wide onClose={onClose} foot={<button className="btn" onClick={onClose}>Close</button>}>
      <div className="form">
        <div className="field">
          <span className="label">1 · SSH config, once per machine</span>
          <Cmd text="walroos ssh-config >> ~/.ssh/config" />
          <span className="help">With <span className="mono">tailscaled</span> in userspace mode, add <span className="mono">--proxy localhost:1055</span>.</span>
        </div>
        <div className="field">
          <span className="label">2 · A shell in the container</span>
          <div className="cmd"><span className="prompt" aria-hidden="true">$</span><code>ssh {alias}</code><CopyBtn text={`ssh ${alias}`} label="Copy SSH command" /></div>
          <span className="help">A stopped workspace starts on connect. <span className="mono">claude</span>, bun and your runtimes live inside.</span>
        </div>
        <div className="field">
          <span className="label">VS Code / Claude Code</span>
          <span className="help">Remote-SSH and pick <span className="mono">{alias}</span>. Ports: <span className="mono">ssh -L 3000:localhost:3000 {alias}</span>.</span>
        </div>
        <div className="field">
          <span className="label">On the node</span>
          <span className="help mono od-truncate">{w.node_name} · {w.image || 'image pending'}</span>
          <span className="help mono od-truncate" title={w.repo_url}>{w.repo_url}{w.branch ? ` (${w.branch})` : ''}</span>
        </div>
      </div>
    </Modal>
  );
}
