import { useCallback, useEffect, useState } from 'react';
import { api, type Detection, type GitHub, type Node, type ProjectStack, type Workspace } from './api';
import { stackLabel } from './stack';
import { ago, Badge, Cmd, Confirm, CopyBtn, EmptyState, EnvModal, ErrorAlert, Field, Icon, Modal, useCan, usePoll, useToast } from './ui';

export function WorkspacesPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const toast = useToast();
  const { me, canUse } = useCan();
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

  // Someone else's workspace (and not the hub's owner): connect info only, no actions.
  const busy = (w: Workspace) => w.state === 'creating' || w.state === 'deleting' || !canUse(w.owner);

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
              <th className="col-grow">Workspace</th><th className="hide-sm">Node</th><th>State</th>
              <th className="hide-lg">Stack</th><th className="hide-sm">Created</th>
              <th className="hide-xs"><span className="sr-only">Actions</span></th><th className="show-xs"><span className="sr-only">More</span></th>
            </tr></thead>
            <tbody>
              {list.map(w => (
                <tr key={w.id}>
                  <td className="col-grow"><div className="cell-lead">
                    <span className={`lead${w.state === 'running' ? '' : ' off'}`} aria-hidden="true"><Icon n="workspace" /></span>
                    <div className="cell-stack">
                      <span className="cell-title mono" title={w.name}>{w.name}</span>
                      <span className="cell-sub mono od-truncate" title={w.repo_url}>{w.repo_url ? repoLabel(w.repo_url) : 'local git repo'}{w.branch ? ` · ${w.branch}` : ''}{w.owner !== me?.login ? ` · ${w.owner}` : ''}</span>
                    </div>
                  </div></td>
                  <td className="hide-sm mono"><span className="clip" title={w.node_name}>{w.node_name}</span></td>
                  <td><Badge s={w.state} />
                    {w.state === 'failed' && w.error && (
                      <div className="cell-sub od-truncate err-line" title={w.error}>{w.error}</div>
                    )}
                  </td>
                  <td className="hide-lg od-nowrap" title={w.image}>{stackLabel(w.stack, w.image) || <span className="muted">—</span>}</td>
                  <td className="hide-sm muted od-nowrap">{ago(w.created_at)}</td>
                  <td className="ta-r od-nowrap hide-xs">
                    <button className="btn-icon" title="Connect" aria-label={`Connect to ${w.name}`} onClick={() => setConnecting(w)}><Icon n="plug" /></button>
                    {w.state === 'stopped'
                      ? <button className="btn-icon" title="Start" aria-label={`Start ${w.name}`} disabled={busy(w)} onClick={() => op(w, api.startWorkspace)}><Icon n="deploy" /></button>
                      : <button className="btn-icon" title="Stop" aria-label={`Stop ${w.name}`} disabled={busy(w) || w.state !== 'running'} onClick={() => op(w, api.stopWorkspace)}><Icon n="x" /></button>}
                    <button className="btn-icon" title="Environment variables" aria-label={`Environment of ${w.name}`} disabled={!canUse(w.owner)} onClick={() => setEditingEnv(w)}><Icon n="settings" /></button>
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
        <Modal title={acting.name} sub={`${acting.node_name} · ${acting.state}${canUse(acting.owner) ? '' : ` · ${acting.owner}'s`}`} onClose={() => setActing(null)}>
          <div className="sheet-list">
            {([
              ['plug', 'Connect', () => setConnecting(acting), false],
              acting.state === 'stopped'
                ? ['deploy', 'Start', () => op(acting, api.startWorkspace), busy(acting)]
                : ['x', 'Stop', () => op(acting, api.stopWorkspace), busy(acting) || acting.state !== 'running'],
              ['settings', 'Environment variables', () => setEditingEnv(acting), !canUse(acting.owner)],
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
      {editingEnv && (
        <EnvModal title={`Environment · ${editingEnv.name}`} savedMsg="Variables saved: rebuild to apply them"
          sub="Passed to the container as KEY=VALUE. Values are encrypted in the hub; they apply when the container is recreated (Rebuild)."
          load={() => api.workspaceEnv(editingEnv.name)} save={vars => api.setWorkspaceEnv(editingEnv.name, vars)}
          onClose={() => setEditingEnv(null)} onSaved={() => { setEditingEnv(null); load(); }} />
      )}
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
  const [error, setError] = useState<{ title: string; msg: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [found, setFound] = useState<Detection | null>(null);
  const [off, setOff] = useState<string[]>([]); // postCreate tools turned off
  const [dcText, setDcText] = useState('');     // the devcontainer.json that will be sent, editable
  const [fresh, setFresh] = useState(false);     // New project: no repo yet
  const [stacks, setStacks] = useState<ProjectStack[]>([]);
  const [gh, setGh] = useState<GitHub | null>(null);
  const [stack, setStack] = useState('');
  const [version, setVersion] = useState('');
  const [onGitHub, setOnGitHub] = useState(false);
  const { can } = useCan();
  const ghOK = !!gh?.configured && can('settings'); // repos go on the owner's account
  const [priv, setPriv] = useState(true);
  const dev = nodes.filter(n => n.roles.includes('dev') && n.status === 'online');

  useEffect(() => {
    if (!fresh || stacks.length) return;
    api.projectStacks().then(list => {
      setStacks(list);
      if (list[0]) { setStack(list[0].id); setVersion(list[0].versions[0]); }
    }, e => setError({ title: "Couldn't load the stacks", msg: e.message }));
    api.github().then(g => { setGh(g); setOnGitHub(g.configured && can('settings')); }, () => setGh({ configured: false }));
  }, [fresh, stacks.length]);
  const chosen = stacks.find(s => s.id === stack);
  const pickStack = (id: string) => { setStack(id); setVersion(stacks.find(s => s.id === id)?.versions[0] ?? ''); };

  const generated = !!found?.devcontainer;
  let dcError = '';
  let dc: Record<string, unknown> | undefined;
  if (generated) {
    try { dc = JSON.parse(dcText); } catch (e) { dcError = (e as Error).message; }
  }

  const detect = () => {
    setDetecting(true); setError(null);
    api.detect(repo, branch, node).then(d => {
      setFound(d); setOff([]); setDcText(devcontainerText(d, []));
      if (!name) setName(suggestName(repo));
      if (!branch) setBranch(d.branch);
    }, e => setError({ title: "Couldn't detect", msg: e.message })).finally(() => setDetecting(false));
  };
  const toggle = (id: string) => {
    const next = off.includes(id) ? off.filter(x => x !== id) : [...off, id];
    setOff(next);
    if (found) setDcText(devcontainerText(found, next));
  };

  const submit = () => {
    setBusy(true);
    if (fresh) {
      api.createFromScratch({ name, stack, version, github: onGitHub, private: priv, node })
        .then(w => { toast(onGitHub ? `${w.name}: repo created, workspace on its way` : `${w.name} is being created`); onCreated(); },
          e => { setError({ title: "Couldn't create it", msg: e.message }); setBusy(false); });
      return;
    }
    api.createWorkspace({ name, repo_url: repo, branch, devcontainer_path: generated ? '' : dcPath, node, devcontainer: dc, stack: found?.stack })
      .then(w => { toast(`${w.name} is being created`); onCreated(); },
        e => { setError({ title: "Couldn't create it", msg: e.message }); setBusy(false); });
  };

  return (
    <Modal title="New workspace" wide onClose={onClose}
      sub={fresh
        ? 'A project with no repo yet: it starts with a devcontainer.json for the stack and a README, on a node with the dev role.'
        : 'Cloned from a Git repo and run on a node with the dev role. A devcontainer.json is used when present, else the walroos base image.'}
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !name || (fresh ? !stack || !version : !repo || !!dcError)} onClick={submit}>
          {busy ? 'Creating…' : 'Create workspace'}
        </button>
      </>}>
      {error && <ErrorAlert title={error.title} msg={error.msg} />}
      <div className="form">
        <div className="seg" role="group" aria-label="Source">
          <button type="button" aria-pressed={!fresh} onClick={() => { setFresh(false); setError(null); }}>{!fresh && <Icon n="check" />}From repo</button>
          <button type="button" aria-pressed={fresh} onClick={() => { setFresh(true); setError(null); }}>{fresh && <Icon n="check" />}From scratch</button>
        </div>
        {fresh ? <>
          <Field label="Name" hint={<>DNS label: the repo's name and the SSH user, <span className="mono">{name || 'myproject'}.walroos</span>.</>}>
            <input key="pname" className="input mono" value={name} onChange={e => setName(e.target.value)} placeholder="myproject" autoFocus />
          </Field>
          <div className="field-row">
            <Field label="Stack">
              <select className="input" value={stack} onChange={e => pickStack(e.target.value)} disabled={!stacks.length}>
                {stacks.map(s => <option key={s.id} value={s.id}>{s.label}</option>)}
              </select>
            </Field>
            <Field label={chosen?.version_of ? `${chosen.version_of} version` : 'Version'}>
              <select className="input mono" value={version} onChange={e => setVersion(e.target.value)} disabled={!chosen}>
                {chosen?.versions.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </Field>
          </div>
          <div className="field">
            <span className="label" id="where-h">Repository</span>
            <div className="radio-cards radio-cards-2" role="radiogroup" aria-labelledby="where-h">
              <label className={`radio-card${ghOK ? '' : ' is-off'}`}>
                <span className="rc-h"><input type="radio" name="where" value="github" aria-label="On GitHub" checked={onGitHub} disabled={!ghOK} onChange={() => setOnGitHub(true)} />On GitHub</span>
                <span className="help">{!can('settings')
                  ? <>Only the hub's owner can create repos on {gh?.login ? <span className="mono">{gh.login}</span> : 'their account'}.</>
                  : gh?.configured
                  ? <>Created as <span className="mono">{gh.login}</span>, with the first commit pushed.</>
                  : <>Needs a token: add one in <a href="#/settings">Settings</a>.</>}</span>
              </label>
              <label className="radio-card">
                <span className="rc-h"><input type="radio" name="where" value="local" aria-label="Local git repo" checked={!onGitHub} onChange={() => setOnGitHub(false)} />Local git repo</span>
                <span className="help"><span className="mono">git init</span> on the node with the files staged. Commit, add <span className="mono">origin</span> and push when you're ready.</span>
              </label>
            </div>
          </div>
          <div className="field-row">
            {onGitHub && (
              <Field label="Visibility">
                <div className="seg" role="group" aria-label="Visibility">
                  <button type="button" aria-pressed={priv} onClick={() => setPriv(true)}>{priv && <Icon n="check" />}Private</button>
                  <button type="button" aria-pressed={!priv} onClick={() => setPriv(false)}>{!priv && <Icon n="check" />}Public</button>
                </div>
              </Field>
            )}
            <Field label="Node" hint={dev.length === 0 ? <span style={{ color: 'var(--bad)' }}>No online node with the dev role.</span> : undefined}>
              <select className="input" value={node} onChange={e => setNode(e.target.value)}>
                <option value="">Auto (least loaded)</option>
                {dev.map(n => <option key={n.id} value={n.name}>{n.name}</option>)}
              </select>
            </Field>
          </div>
          {onGitHub && priv && <p className="help">The node clones with its own SSH key: for a private repo it needs one with access to your account.</p>}
        </> : <>
        <Field label="Repository" hint="The node clones with its own git: public repos, or ones its SSH agent can read. Detect proposes a devcontainer for the stack.">
          <div className="input-btn">
            <input className="input mono" value={repo} onChange={e => { setRepo(e.target.value); setFound(null); }} placeholder="git@github.com:me/app.git" autoFocus />
            <button type="button" className="btn btn-tonal" disabled={!repo || detecting} onClick={detect}>
              {detecting ? <><Icon n="refresh" cls="spin" />Detecting…</> : 'Detect'}
            </button>
          </div>
        </Field>
        {found && <DetectedPanel d={found} off={off} toggle={toggle} dcText={dcText} setDcText={setDcText} dcError={dcError} onDrop={() => setFound(null)} />}
        <Field label="Name" hint={<>DNS label. It's also the SSH user: <span className="mono">{name || 'myproject'}.walroos</span>.</>}>
          <input className="input mono" value={name} onChange={e => setName(e.target.value)} placeholder="myproject" />
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
        {!generated && (
          <Field label={<>devcontainer.json <span className="muted">(optional)</span></>} hint="Leave empty to use the standard location, or the base image if the repo has none.">
            <input className="input mono" value={dcPath} onChange={e => setDcPath(e.target.value)} placeholder=".devcontainer/devcontainer.json" />
          </Field>
        )}
        </>}
      </div>
    </Modal>
  );
}

// The generated config with the turned-off tools' installs left out of postCreateCommand.
function devcontainerText(d: Detection, off: string[]): string {
  const { postCreateCommand: _, ...dc } = d.devcontainer ?? {};
  const cmds = d.tools.filter(t => t.install && !off.includes(t.id)).map(t => t.install);
  return JSON.stringify(cmds.length ? { ...dc, postCreateCommand: cmds.join(' && ') } : dc, null, 2);
}

// github.com/me/My_App.git → my-app
const suggestName = (repo: string) =>
  (repo.replace(/\/+$/, '').split(/[/:]/).pop() ?? '').replace(/\.git$/, '').toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63);

function DetectedPanel({ d, off, toggle, dcText, setDcText, dcError, onDrop }: {
  d: Detection; off: string[]; toggle: (id: string) => void; dcText: string; setDcText: (s: string) => void; dcError: string; onDrop: () => void;
}) {
  const title = d.stack === 'devcontainer' ? "Uses the repo's devcontainer" : d.stack === 'none' ? 'No stack detected' : `Detected ${d.stack}`;
  return (
    <section className="detected" aria-label="Detected stack">
      <div className="detected-head">
        <div className="detected-t">
          <strong>{title}</strong>
          {d.image && <span className="mono muted od-truncate" title={d.image}>{d.image}{d.remote_user ? ` · ${d.remote_user}` : ''}</span>}
        </div>
        {d.devcontainer && <button type="button" className="btn btn-ghost" onClick={onDrop}>Don't use</button>}
      </div>
      {d.devcontainer && d.tools.length > 0 && (
        <ul className="tools">
          {d.tools.map(t => (
            <li key={t.id}>
              <label className="check">
                <input type="checkbox" checked={t.source === 'image' || !off.includes(t.id)} disabled={t.source === 'image'} onChange={() => toggle(t.id)} />
                <span>{t.label}{t.version && <span className="mono muted"> {t.version}</span>}</span>
              </label>
              <span className="chip">{t.source === 'image' ? 'in image' : 'postCreate'}</span>
            </li>
          ))}
        </ul>
      )}
      {d.notes?.map(n => <p key={n} className="help">{n}</p>)}
      {d.devcontainer && (
        <details className="disclosure">
          <summary>devcontainer.json</summary>
          <div>
            <textarea className="mono" rows={10} value={dcText} onChange={e => setDcText(e.target.value)} aria-invalid={!!dcError} aria-label="Generated devcontainer.json" spellCheck={false} />
            <span className={dcError ? 'err' : 'help'}>{dcError || "Sent with the workspace, never written to the repo. Toggling a tool regenerates it."}</span>
          </div>
        </details>
      )}
    </section>
  );
}

function ConnectModal({ w, onClose }: { w: Workspace; onClose: () => void }) {
  const alias = `${w.name}.walroos`;
  return (
    <Modal title={`Connect to ${w.name}`} wide onClose={onClose}>
      <div className="form">
        <div className="field">
          <span className="label">1 · SSH config, again when you add a workspace</span>
          {/* The CLI defaults to walroos-hub; the hub you opened this page on may have another name. */}
          <Cmd text={`walroos ssh-config --hub ${window.location.host} --write`} />
          <span className="help">Keeps every workspace in a walroos section of <span className="mono">~/.ssh/config</span>. With <span className="mono">tailscaled</span> in userspace mode it goes through its proxy on <span className="mono">localhost:1055</span> by itself.</span>
        </div>
        <div className="field">
          <span className="label">2 · A shell in the container</span>
          <div className="cmd"><span className="prompt" aria-hidden="true">$</span><code>ssh {alias}</code><CopyBtn text={`ssh ${alias}`} label="Copy SSH command" /></div>
          <span className="help">A stopped workspace starts on connect. <span className="mono">claude</span>, bun and your runtimes live inside.</span>
        </div>
        <div className="field">
          <span className="label">3 · Your editor, opened on the repo</span>
          {w.folder ? <>
            {/* Without a path, Zed and VS Code open $HOME: hand them the repo folder. */}
            <Cmd text={`zed ssh://${alias}${w.folder}`} />
            <Cmd text={`code --remote ssh-remote+${alias} ${w.folder}`} />
          </> : (
            <span className="help">Connect to <span className="mono">{alias}</span> and open <span className="mono">/workspaces/&lt;repo&gt;</span> (the node didn't answer, so the exact folder isn't known).</span>
          )}
          <span className="help">Zed's <em>Open Remote</em> lists every workspace with its repo after this (run it again when you add one):</span>
          <Cmd text={`walroos ssh-config --hub ${window.location.host} --zed --write`} />
          <span className="help">Claude Code: Remote-SSH to <span className="mono">{alias}</span>. Ports: <span className="mono">ssh -L 3000:localhost:3000 {alias}</span>.</span>
        </div>
        <div className="field">
          <span className="label">On the node</span>
          <span className="help mono od-truncate">{w.node_name} · {w.image || 'image pending'}</span>
          <span className="help mono od-truncate" title={w.repo_url}>{w.repo_url || 'Local git repo: add origin and push from inside'}{w.branch ? ` (${w.branch})` : ''}</span>
        </div>
      </div>
    </Modal>
  );
}
