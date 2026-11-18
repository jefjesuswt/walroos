import { useCallback, useEffect, useState } from 'react';
import { api, type EnvVar, type Node, type Workspace } from './api';
import { ago, Badge, Cmd, Confirm, CopyBtn, EmptyState, Field, Icon, Modal, usePoll, useToast } from './ui';

export function WorkspacesPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const toast = useToast();
  const [list, setList] = useState<Workspace[] | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [error, setError] = useState('');
  const [connecting, setConnecting] = useState<Workspace | null>(null);
  const [removing, setRemoving] = useState<Workspace | null>(null);
  const [rebuilding, setRebuilding] = useState<Workspace | null>(null);
  const [editingEnv, setEditingEnv] = useState<Workspace | null>(null);
  const [acting, setActing] = useState<Workspace | null>(null); // phones: the row's actions in a bottom sheet

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
              <th className="hide-lg">Image</th><th className="hide-sm">Created</th>
              <th className="hide-xs"><span className="sr-only">Actions</span></th><th className="show-xs"><span className="sr-only">More</span></th>
            </tr></thead>
            <tbody>
              {list.map(w => (
                <tr key={w.id}>
                  <td><div className="cell-lead">
                    <span className={`lead${w.state === 'running' ? '' : ' off'}`} aria-hidden="true"><Icon n="workspace" /></span>
                    <div className="cell-stack">
                      <span className="cell-title mono od-nowrap">{w.name}</span>
                      <span className="cell-sub mono od-truncate" title={w.repo_url}>{repoLabel(w.repo_url)}{w.branch ? ` · ${w.branch}` : ''}</span>
                    </div>
                  </div></td>
                  <td className="hide-sm mono">{w.node_name}</td>
                  <td><Badge s={w.state} />
                    {w.state === 'failed' && w.error && (
                      <div className="cell-sub od-truncate err-line" title={w.error}>{w.error}</div>
                    )}
                  </td>
                  <td className="hide-lg mono od-truncate" style={{ maxWidth: 220 }} title={w.image}>{w.image || <span className="muted">—</span>}</td>
                  <td className="hide-sm muted od-nowrap">{ago(w.created_at)}</td>
                  <td className="ta-r od-nowrap hide-xs">
                    <button className="btn-icon" title="Connect" aria-label={`Connect to ${w.name}`} onClick={() => setConnecting(w)}><Icon n="plug" /></button>
                    {w.state === 'stopped'
                      ? <button className="btn-icon" title="Start" aria-label={`Start ${w.name}`} disabled={busy(w)} onClick={() => op(w, api.startWorkspace)}><Icon n="deploy" /></button>
                      : <button className="btn-icon" title="Stop" aria-label={`Stop ${w.name}`} disabled={busy(w) || w.state !== 'running'} onClick={() => op(w, api.stopWorkspace)}><Icon n="x" /></button>}
                    <button className="btn-icon" title="Environment variables" aria-label={`Environment of ${w.name}`} onClick={() => setEditingEnv(w)}><Icon n="settings" /></button>
                    <button className="btn-icon" title="Rebuild (keeps the clone and $HOME)" aria-label={`Rebuild ${w.name}`} disabled={busy(w)} onClick={() => setRebuilding(w)}><Icon n="refresh" /></button>
                    <button className="btn-icon" title="Delete" aria-label={`Delete ${w.name}`} disabled={busy(w)} onClick={() => setRemoving(w)}><Icon n="trash" /></button>
                  </td>
                  <td className="ta-r show-xs">
                    <button className="btn-icon" aria-label={`Actions for ${w.name}`} onClick={() => setActing(w)}><Icon n="more" /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {newOpen && <NewWorkspaceModal nodes={nodes} onClose={() => setNewOpen(false)} onCreated={() => { setNewOpen(false); load(); }} />}
      {acting && (
        <Modal title={acting.name} sub={`${acting.node_name} · ${acting.state}`} onClose={() => setActing(null)}>
          <div className="sheet-list">
            {([
              ['plug', 'Connect', () => setConnecting(acting), false],
              acting.state === 'stopped'
                ? ['deploy', 'Start', () => op(acting, api.startWorkspace), busy(acting)]
                : ['x', 'Stop', () => op(acting, api.stopWorkspace), busy(acting) || acting.state !== 'running'],
              ['settings', 'Environment variables', () => setEditingEnv(acting), false],
              ['refresh', 'Rebuild', () => setRebuilding(acting), busy(acting)],
              ['trash', 'Delete', () => setRemoving(acting), busy(acting)],
            ] as [string, string, () => void, boolean][]).map(([icon, label, run, off]) => (
              <button key={label} className={label === 'Delete' ? 'danger' : ''} disabled={off} onClick={() => { setActing(null); run(); }}>
                <Icon n={icon} />{label}
              </button>
            ))}
          </div>
        </Modal>
      )}
      {connecting && <ConnectModal w={connecting} onClose={() => setConnecting(null)} />}
      {editingEnv && <EnvModal w={editingEnv} onClose={() => setEditingEnv(null)} onSaved={() => { setEditingEnv(null); load(); }} />}
      {rebuilding && (
        <Confirm title={`Rebuild ${rebuilding.name}?`} confirmLabel="Rebuild" onClose={() => setRebuilding(null)}
          onConfirm={() => {
            const w = rebuilding; setRebuilding(null);
            api.rebuildWorkspace(w.name).then(() => { toast(`${w.name} is rebuilding`); load(); }, e => toast(e.message, 'bad'));
          }}>
          <p className="muted">The container and any image built from the repo are recreated; a changed <span className="mono">devcontainer.json</span> or a new base image is picked up. The clone and <span className="mono">$HOME</span> (mise runtimes, <span className="mono">~/.claude</span>, caches) stay.</p>
        </Confirm>
      )}
      {removing && (
        <Confirm title={`Delete ${removing.name}?`} confirmLabel="Delete workspace" danger onClose={() => setRemoving(null)} onConfirm={() => remove(removing)}>
          <p className="muted">This removes the container <strong>and</strong> its volumes: the clone and everything in <span className="mono">$HOME</span> (mise runtimes, <span className="mono">~/.claude</span>, caches). It can't be undone.</p>
        </Confirm>
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
        <Field label="Name" hint={<>DNS label. It's also the SSH user: <span className="mono">{name || 'myproject'}.walroos</span>.</>}>
          <input className="input mono" value={name} onChange={e => setName(e.target.value)} placeholder="myproject" autoFocus />
        </Field>
        <Field label="Repository" hint="The node clones with its own git: public repos, or ones its SSH agent can read.">
          <input className="input mono" value={repo} onChange={e => setRepo(e.target.value)} placeholder="git@github.com:me/app.git" />
        </Field>
        <div className="field-row">
          <Field label={<>Branch <span className="muted">(optional)</span></>}>
            <input className="input mono" value={branch} onChange={e => setBranch(e.target.value)} placeholder="default branch" />
          </Field>
          <Field label="Node" hint={dev.length === 0 ? <span style={{ color: 'var(--bad)' }}>No online node with the dev role.</span> : undefined}>
            <select className="input" value={node} onChange={e => setNode(e.target.value)}>
              <option value="">Auto (least loaded)</option>
              {dev.map(n => <option key={n.id} value={n.name}>{n.name}</option>)}
            </select>
          </Field>
        </div>
        <Field label={<>devcontainer.json <span className="muted">(optional)</span></>} hint="Leave empty to use the standard location, or the base image if the repo has none.">
          <input className="input mono" value={dcPath} onChange={e => setDcPath(e.target.value)} placeholder=".devcontainer/devcontainer.json" />
        </Field>
      </div>
    </Modal>
  );
}

function ConnectModal({ w, onClose }: { w: Workspace; onClose: () => void }) {
  const alias = `${w.name}.walroos`;
  return (
    <Modal title={`Connect to ${w.name}`} wide onClose={onClose}>
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

function EnvModal({ w, onClose, onSaved }: { w: Workspace; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [vars, setVars] = useState<EnvVar[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => { api.workspaceEnv(w.name).then(setVars, e => setError(e.message)); }, [w.name]); // once: a poll would overwrite what you're typing

  const set = (i: number, patch: Partial<EnvVar>) => setVars(vs => (vs ?? []).map((v, j) => j === i ? { ...v, ...patch } : v));
  const save = () => {
    setBusy(true);
    api.setWorkspaceEnv(w.name, vars ?? []).then(
      () => { toast('Variables saved: rebuild to apply them'); onSaved(); },
      e => { setError(e.message); setBusy(false); },
    );
  };

  return (
    <Modal title={`Environment · ${w.name}`} wide onClose={onClose}
      sub="Passed to the container as KEY=VALUE. Values are encrypted in the hub; they apply when the container is recreated (Rebuild)."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !vars} onClick={save}>{busy ? 'Saving…' : 'Save'}</button>
      </>}>
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't load or save</strong><span className="muted">{error}</span></div></div>}
      {!vars ? <div className="skel-row"><i className="skel w50" /></div> : (
        <div className="form">
          {vars.length === 0 && <p className="muted">No variables yet.</p>}
          {vars.map((v, i) => (
            <div key={i} className="env-row">
              <input className="input mono" value={v.key} placeholder="KEY" aria-label="Name"
                onChange={e => set(i, { key: e.target.value.toUpperCase() })} />
              <input className="input mono" value={v.value} placeholder="value" aria-label="Value"
                type={v.secret ? 'password' : 'text'} onChange={e => set(i, { value: e.target.value })} />
              <label className="check" title="Mask this value in the UI">
                <input type="checkbox" checked={v.secret} onChange={e => set(i, { secret: e.target.checked })} />secret
              </label>
              <button className="btn-icon" aria-label="Remove" title="Remove" onClick={() => setVars(vs => (vs ?? []).filter((_, j) => j !== i))}><Icon n="trash" /></button>
            </div>
          ))}
          <div>
            <button className="btn" onClick={() => setVars(vs => [...(vs ?? []), { key: '', value: '', secret: false }])}><Icon n="plus" />Add variable</button>
          </div>
          <p className="help">Names: A–Z, 0–9 and _, starting with a letter or _. The last save replaces the whole set.</p>
        </div>
      )}
    </Modal>
  );
}
