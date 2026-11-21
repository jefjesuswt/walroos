import { useCallback, useEffect, useRef, useState } from 'react';
import { api, type App, type Cloudflare, type Deployment, type Node, type Volume } from './api';
import { BrandLogo } from './logos';
import { ago, Badge, Confirm, EmptyState, EnvEditor, ErrorAlert, Field, Icon, Modal, ProjectField, Tabs, useCan, usePoll, useToast, useTrail } from './ui';

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


/* ---------- one app ---------- */
export type AppTab = 'general' | 'environment' | 'domains' | 'deployments' | 'logs' | 'advanced';
const TABS: [AppTab, string][] = [['general', 'General'], ['environment', 'Environment'], ['domains', 'Domains'],
  ['deployments', 'Deployments'], ['logs', 'Logs'], ['advanced', 'Advanced']];
const draft = (a: App) => !a.repo_url && !a.image;

export function AppDetail({ name }: { name: string }) {
  const { can } = useCan();
  const toast = useToast();
  const [a, setA] = useState<App | null>(null);
  const [deps, setDeps] = useState<Deployment[]>([]);
  const [error, setError] = useState('');
  const [tab, setTab] = useState<AppTab>('general');
  const [deploying, setDeploying] = useState(false);
  const [editingDomains, setEditingDomains] = useState(false);
  const [editingVolumes, setEditingVolumes] = useState(false);
  const [purge, setPurge] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [open, setOpen] = useState<string | null>(null); // deployment id in the log modal

  const load = useCallback(() => {
    api.app(name).then(x => { setA(x); setError(''); }, e => setError(e.message));
    api.deployments(name).then(setDeps, () => undefined);
  }, [name]);
  usePoll(load, settling(deps[0]) ? 2000 : 8000);
  useTrail(a ? [{ label: 'Projects', href: '#/projects' }, { label: a.project, href: `#/projects/${a.project}` }] : null);

  if (error && !a) {
    return <EmptyState icon="alert" title={error === 'app not found' ? 'No such app' : "Couldn't load the app"} text={error}
      action={<a className="btn" href="#/projects">Back to projects</a>} />;
  }
  if (!a) return <div className="skel-list" aria-busy="true" aria-label="Loading"><div className="skel-row"><i className="skel w30" /><i className="skel w50" /></div></div>;

  const [s, label] = draft(a) ? ['stopped', 'no source yet'] : appState(a);
  const canEdit = can('apps');
  const remove = () => api.deleteApp(a.name, purge).then(
    () => { toast(`${a.name} deleted`); location.hash = `#/projects/${a.project}`; },
    e => { setRemoving(false); toast(e.message, 'bad'); },
  );

  return (
    <>
      <div className="page-head">
        <div className="ph-text">
          <div className="title-row"><span className="svc-logo" aria-hidden="true">{draft(a) ? <Icon n="apps" /> : <BrandLogo name={a.image ? 'docker' : 'git'} />}</span><h1 className="mono">{a.name}</h1><Badge s={s} label={label} /></div>
          <div className="ph-meta">
            {a.description && <span>{a.description}</span>}
            <span><Icon n="nodes" /><span className="mono">{a.node_name}</span></span>
            {a.running && <span>live <span className="sha">{short(a.running.sha)}</span></span>}
          </div>
        </div>
        {canEdit && <div className="ph-actions">
          <button className="btn btn-primary" disabled={a.node_down || draft(a)} title={draft(a) ? 'Set its source first' : undefined} onClick={() => setDeploying(true)}><Icon n="deploy" />Deploy</button>
        </div>}
      </div>

      {a.node_down && (
        <div className="alert alert-bad" role="alert"><Icon n="alert" />
          <div className="alert-text"><strong>{a.node_name} isn't answering</strong><span className="muted">Deploys and logs wait for it. The app keeps running if the node is up but the hub can't reach it.</span></div>
        </div>
      )}

      <Tabs tabs={TABS} value={tab} onChange={setTab} />

      {tab === 'general' && <SourceCard app={a} canEdit={canEdit} onSaved={x => { setA(x); toast(draft(a) ? 'Source set: deploy it when ready' : 'Source saved: it applies on the next deploy'); }} />}

      {tab === 'environment' && (canEdit ? (
        <section className="card" aria-labelledby="env-h">
          <div className="card-head"><div><h2 id="env-h">Environment</h2>
            <p className="muted">Passed to the container as KEY=VALUE from a 0600 file on the node, never in the command line. Encrypted in the hub; they apply on the next deploy.</p></div></div>
          <div className="card-body form"><EnvEditor load={() => api.appEnv(a.name)} save={vars => api.setAppEnv(a.name, vars)} savedMsg="Variables saved: deploy to apply them" /></div>
        </section>
      ) : <div className="card card-pad muted">Your role can't see this app's variables: they may hold secrets.</div>)}

      {tab === 'domains' && (
        <section className="card" aria-labelledby="domains-h">
          <div className="card-head">
            <div><h2 id="domains-h">Domains</h2><p className="muted">Public, with TLS, through {a.node_name}'s Cloudflare Tunnel. No ports open on the node.</p></div>
            {canEdit && <button className="btn" onClick={() => setEditingDomains(true)}><Icon n="globe" />Edit</button>}
          </div>
          <div className="card-body">
            {a.domains.length === 0 ? <p className="muted">None yet: the app is only reachable inside its project, as <span className="mono">{a.name}:{a.port || 'port'}</span>.</p> : (
              <ul className="domain-list">
                {a.domains.map(d => <li key={d}><a className="mono" href={`https://${d}`} target="_blank" rel="noreferrer">{d}</a></li>)}
              </ul>
            )}
          </div>
        </section>
      )}

      {tab === 'deployments' && (deps.length === 0 ? (
        <div className="card card-pad muted">{draft(a) ? 'Set a source in General, then Deploy.' : <>Nothing deployed yet. <strong>Deploy</strong> {a.image ? 'pulls the image' : 'builds the branch head'} on {a.node_name}.</>}</div>
      ) : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr><th>State</th><th className="col-grow">{a.image ? 'Image' : 'Commit'}</th><th className="hide-sm">Duration</th><th className="hide-xs">When</th></tr></thead>
            <tbody>
              {deps.map(d => (
                <tr key={d.id} style={{ cursor: 'pointer' }} onClick={() => setOpen(d.id)}>
                  <td><Badge s={d.state} /></td>
                  <td className="col-grow"><div className="cell-stack">
                    <span className="od-row">
                      <button className="sha link-btn" aria-label={`Log of deployment ${d.id}`}>{short(d.sha) || (a.image ? 'pull' : 'branch head')}</button>
                      {isCurrent(a, d) && <span className="tag-current">current</span>}
                    </span>
                    {d.state === 'failed' && d.error && <span className="cell-sub err-line" title={d.error}>{d.error}</span>}
                  </div></td>
                  <td className="hide-sm mono">{settling(d) ? '…' : duration(d)}</td>
                  <td className="hide-xs muted od-nowrap">{ago(d.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}

      {tab === 'logs' && <RuntimeLogs app={a} />}

      {tab === 'advanced' && <>
        <section className="card" aria-labelledby="volumes-h">
          <div className="card-head">
            <div><h2 id="volumes-h">Volumes</h2><p className="muted">Data that outlives releases. With any, a deploy stops the running release before starting the new one.</p></div>
            {canEdit && <button className="btn" onClick={() => setEditingVolumes(true)}><Icon n="settings" />Edit</button>}
          </div>
          <div className="card-body">
            {a.volumes.length === 0 ? <p className="muted">None: every deploy starts from the image's files.</p> : (
              <ul className="domain-list">
                {a.volumes.map(v => <li key={v.target} className="mono od-truncate">{volumeLine(v)}</li>)}
              </ul>
            )}
          </div>
        </section>
        <section className="card" aria-labelledby="run-h">
          <div className="card-head"><div><h2 id="run-h">Runtime</h2><p className="muted">What runs on {a.node_name} now.</p></div></div>
          <div className="card-body">
            <dl className="kv">
              <dt>Container</dt><dd className="mono">{a.running?.container ?? '—'}</dd>
              <dt>Image</dt><dd className="mono">{a.running?.image ?? '—'}</dd>
              <dt>In the project</dt><dd className="mono">{a.name}:{a.port || '—'}</dd>
              <dt>Limits</dt><dd>2 GB of memory, 2048 processes</dd>
            </dl>
          </div>
        </section>
        {canEdit && (
          <section className="card danger-zone" aria-labelledby="danger-h">
            <div className="card-head">
              <div><h2 id="danger-h">Delete app</h2><p className="muted">Stops it on {a.node_name} and removes its images, environment and deployment history.</p></div>
              <button className="btn btn-danger" onClick={() => setRemoving(true)}><Icon n="trash" />Delete</button>
            </div>
          </section>
        )}
      </>}

      {deploying && <DeployModal app={a} onClose={() => setDeploying(false)} onQueued={d => { setDeploying(false); load(); setTab('deployments'); setOpen(d.id); }} />}
      {open && <DeploymentModal key={open} app={a} id={open} onClose={() => setOpen(null)} onRollback={d => { load(); setOpen(d.id); }} />}
      {editingVolumes && <VolumesModal app={a} onClose={() => setEditingVolumes(false)} onSaved={() => { setEditingVolumes(false); load(); }} />}
      {editingDomains && <DomainsModal app={a} onClose={() => setEditingDomains(false)} onSaved={() => { setEditingDomains(false); load(); }} />}
      {removing && (
        <Confirm title={`Delete ${a.name}?`} confirmLabel="Delete app" danger onClose={() => setRemoving(false)} onConfirm={remove}>
          <p className="muted">Stops the app on <span className="mono">{a.node_name}</span> and removes its images, environment and deployment history. It can't be undone.</p>
          {a.volumes.some(v => v.name) && (
            <label className="check" style={{ marginTop: 'var(--sp3)' }}>
              <input type="checkbox" checked={purge} onChange={e => setPurge(e.target.checked)} />
              Also delete its named volumes. Otherwise an app created again as {a.name} gets them back.
            </label>
          )}
        </Confirm>
      )}
    </>
  );
}

// SourceCard is the General tab: where the app comes from (a repo or a published image) and how
// it's checked. A draft shows the form right away; otherwise a summary with Edit.
function SourceCard({ app, canEdit, onSaved }: { app: App; canEdit: boolean; onSaved: (a: App) => void }) {
  const [editing, setEditing] = useState(draft(app));
  const [fromImage, setFromImage] = useState(!!app.image);
  const [f, setF] = useState({ repo_url: app.repo_url, image: app.image, branch: app.branch, dockerfile: app.dockerfile,
    port: app.port ? String(app.port) : '', health_path: app.health_path });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF(x => ({ ...x, [k]: e.target.value }));
  const save = () => {
    setBusy(true);
    const src = fromImage ? { image: f.image.trim(), port: Number(f.port), health_path: f.health_path }
      : { repo_url: f.repo_url.trim(), branch: f.branch, dockerfile: f.dockerfile, port: Number(f.port), health_path: f.health_path };
    api.setAppSource(app.name, src).then(a => { setBusy(false); setError(''); setEditing(false); onSaved(a); }, e => { setError(e.message); setBusy(false); });
  };

  if (!editing) {
    return (
      <section className="card" aria-labelledby="src-h">
        <div className="card-head">
          <div><h2 id="src-h">{app.image ? 'Published image' : 'Repository'}</h2><p className="muted">{app.image ? 'Pulled again on each deploy; a rollback runs the exact image it got then.' : 'Cloned and built from its Dockerfile on each deploy.'}</p></div>
          {canEdit && <button className="btn" onClick={() => setEditing(true)}><Icon n="settings" />Edit</button>}
        </div>
        <div className="card-body">
          <dl className="kv">
            {app.image ? <><dt>Image</dt><dd className="mono">{app.image}</dd></> : <>
              <dt>Repository</dt><dd className="mono">{app.repo_url}</dd>
              <dt>Branch</dt><dd className="mono">{app.branch || 'default branch'}</dd>
              <dt>Dockerfile</dt><dd className="mono">{app.dockerfile || 'Dockerfile'}</dd>
            </>}
            <dt>Port</dt><dd className="mono">{app.port}</dd>
            <dt>Health check</dt><dd className="mono">GET {app.health_path || '/'} → 2xx/3xx within 90 s</dd>
          </dl>
        </div>
      </section>
    );
  }
  return (
    <section className="card" aria-labelledby="src-h">
      <div className="card-head"><div><h2 id="src-h">Source</h2><p className="muted">Where the app comes from. It applies on the next deploy.</p></div></div>
      <div className="card-body">
        {error && <ErrorAlert title="Couldn't save it" msg={error} />}
        {!canEdit ? <p className="muted">No source yet. Someone who can deploy apps sets it.</p> : (
          <div className="form">
            <div className="seg" role="group" aria-label="Source">
              <button aria-pressed={!fromImage} onClick={() => setFromImage(false)}>{!fromImage && <Icon n="check" />}<BrandLogo name="git" size={16} />Git repository</button>
              <button aria-pressed={fromImage} onClick={() => setFromImage(true)}>{fromImage && <Icon n="check" />}<BrandLogo name="docker" size={16} />Docker image</button>
            </div>
            {fromImage ? (
              <Field label="Image" hint="Fully qualified, with its registry.">
                <input key="image" className="input mono" value={f.image} onChange={set('image')} placeholder="ghcr.io/owner/app:latest" autoFocus />
              </Field>
            ) : <>
              <Field label="Repository" hint="The node clones with its own git: public repos, or ones its SSH key can read.">
                <input key="repo" className="input mono" value={f.repo_url} onChange={set('repo_url')} placeholder="https://github.com/me/app.git" autoFocus />
              </Field>
              <div className="field-row">
                <Field label={<>Branch <span className="muted">(optional)</span></>}>
                  <input className="input mono" value={f.branch} onChange={set('branch')} placeholder="default branch" />
                </Field>
                <Field label={<>Dockerfile <span className="muted">(optional)</span></>}>
                  <input className="input mono" value={f.dockerfile} onChange={set('dockerfile')} placeholder="Dockerfile" />
                </Field>
              </div>
            </>}
            <div className="field-row">
              <Field label="Port" hint="What the container listens on.">
                <input className="input mono" value={f.port} onChange={set('port')} placeholder="3000" inputMode="numeric" />
              </Field>
              <Field label={<>Health check path <span className="muted">(optional)</span></>} hint="Must answer 2xx or 3xx within 90 s.">
                <input className="input mono" value={f.health_path} onChange={set('health_path')} placeholder="/" />
              </Field>
            </div>
            <div className="od-row" style={{ gap: 'var(--sp2)' }}>
              <button className="btn btn-primary" disabled={busy || !(fromImage ? f.image : f.repo_url) || !Number(f.port)} onClick={save}><Icon n="check" />{busy ? 'Saving…' : 'Save'}</button>
              {!draft(app) && <button className="btn" onClick={() => setEditing(false)}>Cancel</button>}
            </div>
          </div>
        )}
      </div>
    </section>
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

  const { can } = useCan();
  const canRollback = can('apps') && d?.state === 'succeeded' && !!d.image && !isCurrent(app, d) && !app.node_down;
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

// Compose-like: name:/target for a podman volume, ./dir:/target for a directory under the
// node's ~/walroos-data; :ro at the end mounts it read-only.
const volumeLine = (v: Volume) => `${v.name ?? `./${v.host_path}`}:${v.target}${v.ro ? ':ro' : ''}`;
const parseVolume = (line: string): Volume => {
  const [src = '', target = '', opt] = line.split(':');
  const v: Volume = src.startsWith('./') ? { host_path: src.slice(2), target } : { name: src, target };
  return opt === 'ro' ? { ...v, ro: true } : v;
};

function VolumesModal({ app, onClose, onSaved }: { app: App; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [text, setText] = useState(app.volumes.map(volumeLine).join('\n'));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    api.setVolumes(app.name, text.split('\n').map(l => l.trim()).filter(Boolean).map(parseVolume)).then(
      () => { toast('Volumes saved: deploy to apply them'); onSaved(); },
      e => { setError(e.message); setBusy(false); },
    );
  };
  return (
    <Modal title={`Volumes · ${app.name}`} onClose={onClose}
      sub="They apply on the next deploy. Removing one here leaves its data on the node."
      foot={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn btn-primary" disabled={busy} onClick={submit}><Icon n="check" />{busy ? 'Saving…' : 'Save'}</button></>}>
      {error && <ErrorAlert title="Couldn't save them" msg={error} />}
      <div className="form">
        <Field label="One per line" hint={<><span className="mono">name:/path</span> is a podman volume; <span className="mono">./dir:/path</span> is a directory in <span className="mono">~/walroos-data</span> on {app.node_name} (made if missing). Add <span className="mono">:ro</span> for read-only.</>}>
          <textarea className="input mono" rows={4} value={text} onChange={e => setText(e.target.value)} placeholder={'data:/var/lib/data\n./photos:/photos:ro'} spellCheck={false} autoFocus />
        </Field>
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
      {error && <ErrorAlert title="Couldn't save them" msg={error} />}
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
      {error && <ErrorAlert title="Couldn't queue it" msg={error} />}
      {!app.image && <div className="form">
        <Field label={<>Commit <span className="muted">(optional)</span></>} hint={<>Empty deploys the head of <span className="mono">{app.branch || 'the default branch'}</span>. A SHA (7 to 40 hex) pins a commit.</>}>
          <input className="input mono" value={sha} onChange={e => setSha(e.target.value)} placeholder="branch head" autoFocus
            onKeyDown={e => e.key === 'Enter' && !busy && submit()} />
        </Field>
      </div>}
    </Modal>
  );
}

export function NewAppModal({ onClose, onCreated, project = '' }: { onClose: () => void; onCreated: (a: App) => void; project?: string }) {
  const toast = useToast();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [f, setF] = useState({ name: '', description: '', node: '', project });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.nodes().then(setNodes, () => undefined); }, []);
  const prod = nodes.filter(n => n.roles.includes('prod'));
  const submit = () => {
    setBusy(true);
    api.createApp({ name: f.name, description: f.description, node: f.node, project: f.project, port: 0 })
      .then(a => { toast(`${a.name} created: set its source`); onCreated(a); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title="Create application" onClose={onClose} sub="Give it a name. Its source (a Git repository or a Docker image) goes in its General tab."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !f.name} onClick={submit}>{busy ? 'Creating…' : 'Create'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <Field label="Name" hint={<>DNS label. Other services of the project reach it as <span className="mono">{f.name || 'name'}:port</span>.</>}>
          <input className="input mono" value={f.name} onChange={e => setF(x => ({ ...x, name: e.target.value }))} placeholder="api" autoFocus />
        </Field>
        <Field label={<>Description <span className="muted">(optional)</span></>}>
          <input className="input" value={f.description} onChange={e => setF(x => ({ ...x, description: e.target.value }))} placeholder="The backend" />
        </Field>
        <ProjectField value={f.project} onChange={v => setF(x => ({ ...x, project: v }))} locked={!!project} />
        <Field label="Node" hint={prod.length === 0 ? <span style={{ color: 'var(--bad)' }}>No node with the prod role. Join one with <span className="mono">--roles prod</span>.</span> : undefined}>
          <select className="input" value={f.node} onChange={e => setF(x => ({ ...x, node: e.target.value }))}>
            <option value="">Auto (first online)</option>
            {prod.map(n => <option key={n.id} value={n.name} disabled={n.status !== 'online'}>{n.name}{n.status !== 'online' ? ` (${n.status})` : ''}</option>)}
          </select>
        </Field>
      </div>
    </Modal>
  );
}

