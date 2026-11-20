import { useCallback, useEffect, useRef, useState } from 'react';
import { api, type App, type Cloudflare, type Deployment, type Node } from './api';
import { ago, Badge, Confirm, EmptyState, EnvModal, Field, Icon, Modal, usePoll, useToast } from './ui';

// What the app is doing, from the node's live release first: a deployment's state is only
// what the hub saw (it can say failed for a deploy the node finished after a hub restart).
function appState(a: App): [string, string] {
  if (a.node_down) return ['offline', 'node offline'];
  const d = a.last_deployment;
  if (d && (d.state === 'queued' || d.state === 'running')) return ['deploying', d.state === 'queued' ? 'queued' : 'deploying'];
  if (a.running) return a.running.state === 'active' ? ['running', 'running'] : [a.running.state, a.running.state];
  if (d?.state === 'failed') return ['failed', 'deploy failed'];
  return ['stopped', 'not deployed'];
}

const short = (sha: string) => sha.slice(0, 7);
const isCurrent = (a: App, d: Deployment) => a.running?.container === `${a.name}-${d.id}`;
const settling = (d?: Deployment) => d?.state === 'queued' || d?.state === 'running';

function duration(d: Deployment): string {
  const end = d.finished_at ? new Date(d.finished_at).getTime() : Date.now();
  const s = Math.max(0, Math.round((end - new Date(d.created_at).getTime()) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

// github.com/me/app(.git) → me/app
const repoLabel = (url: string) => url.replace(/\.git$/, '').match(/(?:[:/])([^/]+\/[^/]+)$/)?.[1] ?? url;
const source = (a: App) => a.image || `${repoLabel(a.repo_url)}${a.branch ? ` · ${a.branch}` : ''}`;

/* ---------- list ---------- */
export function AppsPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const [list, setList] = useState<App[] | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(() => { api.apps().then(a => { setList(a); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, list?.some(a => settling(a.last_deployment)) ? 2000 : 8000);

  return (
    <>
      <h1 className="sr-only">Apps</h1>
      {list && list.length > 0 && (
        <div className="page-head page-bar">
          <p className="muted page-sub">{list.filter(a => appState(a)[0] === 'running').length} running · {list.length} total</p><span />
        </div>
      )}
      {error && !list ? (
        <div className="state state-err" role="alert">
          <Icon n="alert" cls="ic-lg" /><h3>Couldn't load apps</h3><p className="muted">{error}</p>
          <button className="btn" onClick={load}><Icon n="refresh" />Retry</button>
        </div>
      ) : !list ? (
        <div className="skel-list" aria-busy="true" aria-label="Loading">
          {[0, 1].map(i => <div key={i} className="skel-row"><i className="skel w30" /><i className="skel w50" /><i className="skel w15" /></div>)}
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon="apps" title="No apps yet"
          text="An app is a Git repo with a Dockerfile, or a published image, run on a node with the prod role. New releases replace the old one only once they answer."
          action={<button className="btn btn-primary" onClick={() => setNewOpen(true)}><Icon n="plus" />New app</button>} />
      ) : (
        <div className="grid-cards">
          {list.map(a => {
            const [s, label] = appState(a), d = a.last_deployment;
            return (
              <article key={a.id} className="card app-card" onClick={() => { location.hash = `#/apps/${a.name}`; }}>
                <div className="ac-head"><a className="cell-title mono" href={`#/apps/${a.name}`}>{a.name}</a><Badge s={s} label={label} /></div>
                <span className="cell-sub mono od-truncate" title={a.image || a.repo_url}>{source(a)}</span>
                <div className="od-cluster"><span className="chip"><Icon n="nodes" /><span className="mono">{a.node_name}</span></span><span className="chip mono">:{a.port}</span></div>
                {d && (
                  <div className="ac-last">
                    <div className="od-row"><Badge s={d.state} /><span className="sha">{short(d.sha) || '—'}</span><span className="od-fill" /><span className="muted od-nowrap" style={{ fontSize: 'var(--fs-xs)' }}>{ago(d.created_at)}</span></div>
                    {d.state === 'failed' && d.error && <span className="od-truncate err-line" style={{ fontSize: 'var(--fs-sm)' }} title={d.error}>{d.error}</span>}
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}
      {newOpen && <NewAppModal onClose={() => setNewOpen(false)} onCreated={a => { setNewOpen(false); location.hash = `#/apps/${a.name}`; }} />}
    </>
  );
}

/* ---------- one app ---------- */
export function AppDetail({ name }: { name: string }) {
  const toast = useToast();
  const [a, setA] = useState<App | null>(null);
  const [deps, setDeps] = useState<Deployment[]>([]);
  const [error, setError] = useState('');
  const [deploying, setDeploying] = useState(false);
  const [editingEnv, setEditingEnv] = useState(false);
  const [editingDomains, setEditingDomains] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [open, setOpen] = useState<string | null>(null); // deployment id in the log modal

  const load = useCallback(() => {
    api.app(name).then(x => { setA(x); setError(''); }, e => setError(e.message));
    api.deployments(name).then(setDeps, () => undefined);
  }, [name]);
  usePoll(load, settling(deps[0]) ? 2000 : 8000);

  if (error && !a) {
    return <EmptyState icon="alert" title={error === 'app not found' ? 'No such app' : "Couldn't load the app"} text={error}
      action={<a className="btn" href="#/apps">Back to apps</a>} />;
  }
  if (!a) return <div className="skel-list" aria-busy="true" aria-label="Loading"><div className="skel-row"><i className="skel w30" /><i className="skel w50" /></div></div>;

  const [s, label] = appState(a);
  const remove = () => api.deleteApp(a.name).then(
    () => { toast(`${a.name} deleted`); location.hash = '#/apps'; },
    e => { setRemoving(false); toast(e.message, 'bad'); },
  );

  return (
    <>
      <div className="page-head">
        <div className="ph-text">
          <div className="title-row"><h1 className="mono">{a.name}</h1><Badge s={s} label={label} /></div>
          <div className="ph-meta">
            <span><Icon n="nodes" /><span className="mono">{a.node_name}</span></span>
            <span className="mono od-truncate" title={a.image || a.repo_url}>{source(a)}</span>
            {a.running && <span>live <span className="sha">{short(a.running.sha)}</span></span>}
          </div>
        </div>
        <div className="ph-actions">
          <button className="btn btn-primary" disabled={a.node_down} onClick={() => setDeploying(true)}><Icon n="deploy" />Deploy</button>
          <button className="btn" onClick={() => setEditingEnv(true)}><Icon n="settings" />Environment</button>
          <button className="btn-icon" aria-label={`Delete ${a.name}`} title="Delete" onClick={() => setRemoving(true)}><Icon n="trash" /></button>
        </div>
      </div>

      {a.node_down && (
        <div className="alert alert-bad" role="alert"><Icon n="alert" />
          <div className="alert-text"><strong>{a.node_name} isn't answering</strong><span className="muted">Deploys and logs wait for it. The app keeps running if the node is up but the hub can't reach it.</span></div>
        </div>
      )}

      <section className="section" aria-labelledby="deps-h">
        <div className="section-head"><h2 id="deps-h">Deployments</h2></div>
        {deps.length === 0 ? (
          <div className="card card-pad muted">Nothing deployed yet. <strong>Deploy</strong> {a.image ? 'pulls the image' : 'builds the branch head'} on {a.node_name}.</div>
        ) : (
          <div className="table-wrap">
            <table className="tbl">
              <thead><tr><th>State</th><th>{a.image ? 'Image' : 'Commit'}</th><th className="hide-sm">Duration</th><th className="hide-xs">When</th></tr></thead>
              <tbody>
                {deps.map(d => (
                  <tr key={d.id} style={{ cursor: 'pointer' }} onClick={() => setOpen(d.id)}>
                    <td><Badge s={d.state} /></td>
                    <td><div className="cell-stack">
                      <span className="od-row">
                        <button className="sha link-btn" aria-label={`Log of deployment ${d.id}`}>{short(d.sha) || (a.image ? 'pull' : 'branch head')}</button>
                        {isCurrent(a, d) && <span className="tag-current">current</span>}
                      </span>
                      {d.state === 'failed' && d.error && <span className="cell-sub od-truncate err-line" style={{ maxWidth: 'min(360px, 40vw)' }} title={d.error}>{d.error}</span>}
                    </div></td>
                    <td className="hide-sm mono">{settling(d) ? '…' : duration(d)}</td>
                    <td className="hide-xs muted od-nowrap">{ago(d.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="card" aria-labelledby="domains-h">
        <div className="card-head">
          <div><h2 id="domains-h">Domains</h2><p className="muted">Public, with TLS, through {a.node_name}'s Cloudflare Tunnel. No ports open on the node.</p></div>
          <button className="btn" onClick={() => setEditingDomains(true)}><Icon n="globe" />Edit</button>
        </div>
        <div className="card-body">
          {a.domains.length === 0 ? <p className="muted">None yet: the app is only reachable inside the node.</p> : (
            <ul className="domain-list">
              {a.domains.map(d => <li key={d}><a className="mono" href={`https://${d}`} target="_blank" rel="noreferrer">{d}</a></li>)}
            </ul>
          )}
        </div>
      </section>

      <RuntimeLogs app={a} />

      <section className="card" aria-labelledby="build-h">
        <div className="card-head"><div><h2 id="build-h">{a.image ? 'Source' : 'Build'}</h2><p className="muted">Recreate the app to change these.</p></div></div>
        <div className="card-body">
          <dl className="kv">
            {a.image ? <><dt>Published image</dt><dd className="mono">{a.image}</dd></> : <>
              <dt>Repository</dt><dd className="mono">{a.repo_url}</dd>
              <dt>Branch</dt><dd className="mono">{a.branch || 'default branch'}</dd>
              <dt>Dockerfile</dt><dd className="mono">{a.dockerfile || 'Dockerfile'}</dd>
            </>}
            <dt>Port</dt><dd className="mono">{a.port}</dd>
            <dt>Health check</dt><dd className="mono">GET {a.health_path || '/'} → 2xx/3xx within 90 s</dd>
            <dt>Container</dt><dd className="mono">{a.running?.container ?? '—'}</dd>
            <dt>Image</dt><dd className="mono">{a.running?.image ?? '—'}</dd>
          </dl>
        </div>
      </section>

      {deploying && <DeployModal app={a} onClose={() => setDeploying(false)} onQueued={d => { setDeploying(false); load(); setOpen(d.id); }} />}
      {open && <DeploymentModal key={open} app={a} id={open} onClose={() => setOpen(null)} onRollback={d => { load(); setOpen(d.id); }} />}
      {editingEnv && (
        <EnvModal title={`Environment · ${a.name}`} savedMsg="Variables saved: deploy to apply them"
          sub="Passed to the container as KEY=VALUE from a 0600 file on the node, never in the command line. Encrypted in the hub; they apply on the next deploy."
          load={() => api.appEnv(a.name)} save={vars => api.setAppEnv(a.name, vars)}
          onClose={() => setEditingEnv(false)} onSaved={() => setEditingEnv(false)} />
      )}
      {editingDomains && <DomainsModal app={a} onClose={() => setEditingDomains(false)} onSaved={() => { setEditingDomains(false); load(); }} />}
      {removing && (
        <Confirm title={`Delete ${a.name}?`} confirmLabel="Delete app" danger onClose={() => setRemoving(false)} onConfirm={remove}>
          <p className="muted">Stops the app on <span className="mono">{a.node_name}</span> and removes its images, environment and deployment history. It can't be undone.</p>
        </Confirm>
      )}
    </>
  );
}

function RuntimeLogs({ app }: { app: App }) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState('');
  const box = useRef<HTMLDivElement>(null);
  const load = useCallback(() => {
    if (app.node_down) return;
    api.appLogs(app.name).then(t => { setText(t); setError(''); }, e => setError(e.message));
  }, [app.name, app.node_down]);
  usePoll(load, 5000);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [text]);

  const lines = (text ?? '').split('\n').filter(l => l.trim());
  return (
    <section className="section" aria-labelledby="logs-h">
      <div className="section-head"><h2 id="logs-h">Logs</h2><span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>last 300 lines of the live release · every 5 s</span></div>
      <div className="log" ref={box} role="log" aria-label="Runtime logs">
        {error ? <div className="log-empty">{error}</div>
          : text === null ? <div className="log-empty">Loading…</div>
          : lines.length === 0 ? <div className="log-empty">{app.running ? 'No output yet.' : 'Nothing is running.'}</div>
          : <div className="log-lines">{lines.map((l, i) => <div key={i} className="ln plain"><span className="t">{l}</span></div>)}</div>}
      </div>
    </section>
  );
}

/* ---------- deployments ---------- */
export function DeploymentsPage() {
  const [list, setList] = useState<Deployment[] | null>(null);
  const [error, setError] = useState('');
  const [open, setOpen] = useState<Deployment | null>(null);
  const [app, setApp] = useState<App | null>(null);
  const load = useCallback(() => { api.deployments().then(d => { setList(d); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, list?.some(settling) ? 2000 : 8000);
  useEffect(() => { if (open?.app_name) api.app(open.app_name).then(setApp, () => setApp(null)); else setApp(null); }, [open]);

  return (
    <>
      <h1 className="sr-only">Deployments</h1>
      {error && !list ? (
        <div className="state state-err" role="alert"><Icon n="alert" cls="ic-lg" /><h3>Couldn't load deployments</h3><p className="muted">{error}</p>
          <button className="btn" onClick={load}><Icon n="refresh" />Retry</button></div>
      ) : !list ? (
        <div className="skel-list" aria-busy="true" aria-label="Loading"><div className="skel-row"><i className="skel w30" /><i className="skel w50" /></div></div>
      ) : list.length === 0 ? (
        <EmptyState icon="deploy" title="No deployments yet" text="Every deploy of every app shows up here, with its build log." action={<a className="btn" href="#/apps">Go to apps</a>} />
      ) : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr><th>State</th><th>App</th><th>Commit</th><th className="hide-sm">Duration</th><th className="hide-xs">When</th></tr></thead>
            <tbody>
              {list.map(d => (
                <tr key={d.id} style={{ cursor: 'pointer' }} onClick={() => setOpen(d)}>
                  <td><Badge s={d.state} /></td>
                  <td><a className="cell-title mono" href={`#/apps/${d.app_name}`} onClick={e => e.stopPropagation()}>{d.app_name}</a></td>
                  <td><div className="cell-stack">
                    <button className="sha link-btn" aria-label={`Log of deployment ${d.id}`}>{short(d.sha) || 'branch head'}</button>
                    {d.state === 'failed' && d.error && <span className="cell-sub od-truncate err-line" style={{ maxWidth: 'min(360px, 40vw)' }} title={d.error}>{d.error}</span>}
                  </div></td>
                  <td className="hide-sm mono">{settling(d) ? '…' : duration(d)}</td>
                  <td className="hide-xs muted od-nowrap">{ago(d.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {open && app && <DeploymentModal key={open.id} app={app} id={open.id} onClose={() => setOpen(null)} onRollback={d => { load(); setOpen({ ...d, app_name: app.name }); }} />}
    </>
  );
}

// DeploymentModal shows one deployment's build log, live while it runs.
function DeploymentModal({ app, id, onClose, onRollback }: {
  app: App; id: string; onClose: () => void; onRollback: (d: Deployment) => void;
}) {
  const toast = useToast();
  const [d, setD] = useState<Deployment | null>(null);
  const [error, setError] = useState('');
  const box = useRef<HTMLDivElement>(null);
  const load = useCallback(() => { api.deployment(app.name, id).then(setD, e => setError(e.message)); }, [app.name, id]);
  usePoll(load, settling(d ?? undefined) || !d ? 1500 : 60000);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [d?.log]);

  const canRollback = d?.state === 'succeeded' && !!d.image && !isCurrent(app, d) && !app.node_down;
  const rollback = () => d && api.rollback(app.name, d.id).then(
    next => { toast(`Rolling back ${app.name} to ${short(d.sha)}`); onRollback(next); },
    e => toast(e.message, 'bad'),
  );
  const lines = (d?.log ?? '').split('\n').filter(l => l !== '');

  return (
    <Modal wide onClose={onClose} title={`${app.name} · ${short(d?.sha ?? '') || 'deploy'}`}
      sub={d ? `${d.state}${isCurrent(app, d) ? ' · current release' : ''} · started ${ago(d.created_at)}${settling(d) ? '' : ` · took ${duration(d)}`}` : 'Loading…'}
      foot={canRollback ? <><button className="btn" onClick={onClose}>Close</button><button className="btn btn-primary" onClick={rollback}><Icon n="rollback" />Roll back to this</button></> : undefined}>
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't load it</strong><span className="muted">{error}</span></div></div>}
      {d?.state === 'failed' && d.error && (
        <div className="alert alert-bad" role="alert"><Icon n="alert" />
          <div className="alert-text"><strong className="mono" style={{ overflowWrap: 'anywhere' }}>{d.error}</strong>
            <span className="muted">{app.running ? <>The previous release keeps serving: <span className="sha">{short(app.running.sha)}</span>.</> : 'Nothing was released.'}</span></div>
        </div>
      )}
      <div className="log" ref={box} role="log" aria-label="Deployment log" style={{ maxHeight: '55dvh' }}>
        {lines.length === 0 ? <div className="log-empty">{d?.state === 'queued' ? 'Waiting for the deploy before it to finish…' : 'No output yet.'}</div>
          : <div className="log-lines">{lines.map((l, i) => (
            <div key={i} className={`ln plain${/never got healthy|^error|failed|fatal/i.test(l) ? ' err' : /^(cloning|building|starting|health check|stopping|rollback)|is live/.test(l) ? '' : ' dim'}`}><span className="t">{l}</span></div>
          ))}</div>}
      </div>
    </Modal>
  );
}

function DomainsModal({ app, onClose, onSaved }: { app: App; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [text, setText] = useState(app.domains.join('\n'));
  const [cf, setCf] = useState<Cloudflare | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.cloudflare().then(setCf, e => setError(e.message)); }, []);
  const submit = () => {
    setBusy(true);
    api.setDomains(app.name, text.split(/[\s,]+/).filter(Boolean)).then(
      () => { toast('Domains saved'); onSaved(); },
      e => { setError(e.message); setBusy(false); },
    );
  };
  return (
    <Modal title={`Domains · ${app.name}`} onClose={onClose}
      sub={`Each one gets a proxied CNAME to the tunnel and a rule to http://${app.name}:${app.port}. Deploys don't touch them.`}
      foot={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn btn-primary" disabled={busy || cf?.configured === false} onClick={submit}><Icon n="check" />{busy ? 'Saving…' : 'Save'}</button></>}>
      {cf?.configured === false && (
        <div className="alert alert-busy" role="status"><Icon n="alert" />
          <div className="alert-text"><strong>Cloudflare isn't set up</strong><span className="muted">Add an API token in <a href="#/settings">Settings</a> first.</span></div>
        </div>
      )}
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't save them</strong><span className="muted">{error}</span></div></div>}
      <div className="form">
        <Field label="Hostnames, one per line" hint={cf?.zones.length ? <>In a zone the token can edit: <span className="mono">{cf.zones.join(', ')}</span>. Empty removes them all.</> : 'Empty removes them all.'}>
          <textarea className="input mono" rows={4} value={text} onChange={e => setText(e.target.value)} placeholder={`${app.name}.example.com`} spellCheck={false} autoFocus />
        </Field>
      </div>
    </Modal>
  );
}

function DeployModal({ app, onClose, onQueued }: { app: App; onClose: () => void; onQueued: (d: Deployment) => void }) {
  const toast = useToast();
  const [sha, setSha] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    api.deployApp(app.name, sha.trim().toLowerCase()).then(
      d => { toast(`${app.name} queued`); onQueued(d); },
      e => { setError(e.message); setBusy(false); },
    );
  };
  return (
    <Modal title={`Deploy ${app.name}`} onClose={onClose}
      sub={`${app.image ? `Pulls ${app.image}` : 'Builds'} on ${app.node_name} and starts next to the running release; the old one stops only once the new one answers.`}
      foot={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn btn-primary" disabled={busy} onClick={submit}><Icon n="deploy" />{busy ? 'Queuing…' : 'Deploy'}</button></>}>
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't queue it</strong><span className="muted">{error}</span></div></div>}
      {!app.image && <div className="form">
        <Field label={<>Commit <span className="muted">(optional)</span></>} hint={<>Empty deploys the head of <span className="mono">{app.branch || 'the default branch'}</span>. A SHA (7 to 40 hex) pins a commit.</>}>
          <input className="input mono" value={sha} onChange={e => setSha(e.target.value)} placeholder="branch head" autoFocus
            onKeyDown={e => e.key === 'Enter' && !busy && submit()} />
        </Field>
      </div>}
    </Modal>
  );
}

function NewAppModal({ onClose, onCreated }: { onClose: () => void; onCreated: (a: App) => void }) {
  const toast = useToast();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [f, setF] = useState({ name: '', repo_url: '', image: '', branch: '', dockerfile: '', port: '', health_path: '', node: '' });
  const [fromImage, setFromImage] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.nodes().then(setNodes, () => undefined); }, []);
  const prod = nodes.filter(n => n.roles.includes('prod'));
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF(x => ({ ...x, [k]: e.target.value }));
  const port = Number(f.port);
  const src = fromImage ? f.image : f.repo_url;

  const submit = (deploy: boolean) => {
    setBusy(true);
    api.createApp(fromImage ? { name: f.name, image: f.image.trim(), port, health_path: f.health_path, node: f.node } : { ...f, image: '', port })
      .then(async a => {
        if (deploy) await api.deployApp(a.name).catch(e => toast(e.message, 'bad'));
        toast(deploy ? `${a.name} created, deploying` : `${a.name} created`);
        onCreated(a);
      }, e => { setError(e.message); setBusy(false); });
  };

  return (
    <Modal title="New app" wide onClose={onClose}
      sub={`${fromImage ? 'Pulled on a node with the prod role, again on each deploy' : 'Cloned on a node with the prod role and built from its Dockerfile'}. It joins the walroos network and publishes no host ports.`}
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn" disabled={busy || !f.name || !src || !port} onClick={() => submit(false)}>Create</button>
        <button className="btn btn-primary" disabled={busy || !f.name || !src || !port || prod.every(n => n.status !== 'online')} onClick={() => submit(true)}>
          <Icon n="deploy" />{busy ? 'Creating…' : 'Create and deploy'}
        </button>
      </>}>
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't create it</strong><span className="muted">{error}</span></div></div>}
      <div className="form">
        <div className="seg" role="group" aria-label="Source">
          <button aria-pressed={!fromImage} onClick={() => setFromImage(false)}>{!fromImage && <Icon n="check" />}Repository</button>
          <button aria-pressed={fromImage} onClick={() => setFromImage(true)}>{fromImage && <Icon n="check" />}Published image</button>
        </div>
        {fromImage ? (
          <Field label="Image" hint="Fully qualified, with its registry. The node pulls it on each deploy; a rollback runs the exact image it got then.">
            <input key="image" className="input mono" value={f.image} onChange={set('image')} placeholder="ghcr.io/owner/app:latest" autoFocus />
          </Field>
        ) : (
          <Field label="Repository" hint="The node clones with its own git: public repos, or ones its SSH agent can read.">
            <input key="repo" className="input mono" value={f.repo_url} onChange={set('repo_url')} placeholder="https://github.com/me/app.git" autoFocus />
          </Field>
        )}
        <div className="field-row">
          <Field label="Name" hint="DNS label; containers are named after it.">
            <input className="input mono" value={f.name} onChange={set('name')} placeholder="my-app"
              onFocus={() => !f.name && src && setF(x => ({ ...x, name: suggestName(fromImage ? x.image.replace(/[:@][^/]*$/, '') : x.repo_url) }))} />
          </Field>
          <Field label="Port" hint="What the container listens on.">
            <input className="input mono" value={f.port} onChange={set('port')} placeholder="3000" inputMode="numeric" />
          </Field>
        </div>
        <div className="field-row">
          {!fromImage && <Field label={<>Branch <span className="muted">(optional)</span></>}>
            <input className="input mono" value={f.branch} onChange={set('branch')} placeholder="default branch" />
          </Field>}
          <Field label="Node" hint={prod.length === 0 ? <span style={{ color: 'var(--bad)' }}>No node with the prod role. Join one with <span className="mono">--roles prod</span>.</span> : undefined}>
            <select className="input" value={f.node} onChange={set('node')}>
              <option value="">Auto (first online)</option>
              {prod.map(n => <option key={n.id} value={n.name} disabled={n.status !== 'online'}>{n.name}{n.status !== 'online' ? ` (${n.status})` : ''}</option>)}
            </select>
          </Field>
        </div>
        <div className="field-row">
          {!fromImage && <Field label={<>Dockerfile <span className="muted">(optional)</span></>}>
            <input className="input mono" value={f.dockerfile} onChange={set('dockerfile')} placeholder="Dockerfile" />
          </Field>}
          <Field label={<>Health check path <span className="muted">(optional)</span></>} hint="Must answer 2xx or 3xx within 90 s.">
            <input className="input mono" value={f.health_path} onChange={set('health_path')} placeholder="/" />
          </Field>
        </div>
      </div>
    </Modal>
  );
}

// github.com/me/My_App.git → my-app
const suggestName = (repo: string) =>
  (repo.replace(/\/+$/, '').split(/[/:]/).pop() ?? '').replace(/\.git$/, '').toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63);
