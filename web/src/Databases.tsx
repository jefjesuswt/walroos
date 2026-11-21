import { useCallback, useEffect, useState } from 'react';
import { api, type App, type Backup, type Database, type Engine, type Node } from './api';
import { ago, Badge, Confirm, CopyBtn, EmptyState, ErrorAlert, Field, Icon, Modal, usePoll, useToast } from './ui';

const engineLabel: Record<string, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', redis: 'Redis' };
const label = (d: Database) => `${engineLabel[d.engine] ?? d.engine} ${d.version}`;

// What the database is doing: the node's systemd state, or why we don't know it.
function dbState(d: Database): [string, string] {
  if (d.node_down) return ['offline', 'node offline'];
  if (d.state === 'active') return ['running', 'running'];
  if (d.state === 'activating') return ['activating', 'starting'];
  return [d.state || 'stopped', d.state || 'unknown'];
}

function size(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(2)} GB`;
}

const schedule = (d: Database) => d.backup_every_hours === 0 ? 'off'
  : d.backup_every_hours % 24 === 0 ? `every ${d.backup_every_hours / 24 === 1 ? 'day' : `${d.backup_every_hours / 24} days`}`
  : `every ${d.backup_every_hours} h`;

function LastBackup({ d }: { d: Database }) {
  if (!d.last_backup_at) return <span className="muted">never</span>;
  if (d.last_backup_error) return <span className="err-line od-truncate" title={d.last_backup_error}>failed {ago(d.last_backup_at)}</span>;
  return <span className="muted">{ago(d.last_backup_at)}</span>;
}

/* ---------- list ---------- */
export function DatabasesPage({ newOpen, setNewOpen }: { newOpen: boolean; setNewOpen: (v: boolean) => void }) {
  const [list, setList] = useState<Database[] | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(() => { api.databases().then(l => { setList(l); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, list?.some(d => d.state === 'activating') ? 2000 : 10000);

  return (
    <>
      <h1 className="sr-only">Databases</h1>
      {list && list.length > 0 && (
        <div className="page-head page-bar">
          <p className="muted page-sub">{list.filter(d => d.state === 'active').length} of {list.length} running</p><span />
        </div>
      )}
      {error && !list ? (
        <div className="state state-err" role="alert">
          <Icon n="alert" cls="ic-lg" /><h3>Couldn't load databases</h3><p className="muted">{error}</p>
          <button className="btn" onClick={load}><Icon n="refresh" />Retry</button>
        </div>
      ) : !list ? (
        <div className="skel-list" aria-busy="true" aria-label="Loading">
          {[0, 1].map(i => <div key={i} className="skel-row"><i className="skel w30" /><i className="skel w50" /><i className="skel w15" /></div>)}
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon="database" title="No databases yet"
          text="PostgreSQL, MySQL, MariaDB or Redis on a node with the prod role, with a generated password and daily backups. Apps on the same node reach it by name."
          action={<button className="btn btn-primary" onClick={() => setNewOpen(true)}><Icon n="plus" />New database</button>} />
      ) : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr>
              <th className="col-grow">Database</th><th className="hide-sm">Node</th><th>State</th>
              <th className="hide-md">Backups</th><th className="hide-xs">Last backup</th>
            </tr></thead>
            <tbody>
              {list.map(d => {
                const [s, l] = dbState(d);
                return (
                  <tr key={d.id} style={{ cursor: 'pointer' }} onClick={() => { location.hash = `#/databases/${d.name}`; }}>
                    <td className="col-grow"><div className="cell-lead">
                      <span className={`lead${s === 'running' ? '' : ' off'}`} aria-hidden="true"><Icon n="database" /></span>
                      <div className="cell-stack">
                        <a className="cell-title mono" href={`#/databases/${d.name}`} title={d.name}>{d.name}</a>
                        <span className="cell-sub">{label(d)}</span>
                      </div>
                    </div></td>
                    <td className="hide-sm mono"><span className="clip" title={d.node_name}>{d.node_name}</span></td>
                    <td><Badge s={s} label={l} /></td>
                    <td className="hide-md muted od-nowrap">{schedule(d)}</td>
                    <td className="hide-xs od-nowrap"><LastBackup d={d} /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {newOpen && <NewDatabaseModal onClose={() => setNewOpen(false)} onCreated={d => { setNewOpen(false); location.hash = `#/databases/${d.name}`; }} />}
    </>
  );
}

function NewDatabaseModal({ onClose, onCreated }: { onClose: () => void; onCreated: (d: Database) => void }) {
  const toast = useToast();
  const [engines, setEngines] = useState<Engine[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [f, setF] = useState({ name: '', engine: 'postgres', version: '', node: '' });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.engines().then(list => { setEngines(list); setF(x => ({ ...x, version: list.find(e => e.id === x.engine)?.versions[0] ?? '' })); },
      e => setError(e.message));
    api.nodes().then(setNodes, () => undefined);
  }, []);
  const prod = nodes.filter(n => n.roles.includes('prod'));
  const engine = engines.find(e => e.id === f.engine);
  const pick = (id: string) => setF(x => ({ ...x, engine: id, version: engines.find(e => e.id === id)?.versions[0] ?? '' }));

  const submit = () => {
    setBusy(true);
    api.createDatabase(f).then(d => { toast(`${d.name} is starting`); onCreated(d); }, e => { setError(e.message); setBusy(false); });
  };

  return (
    <Modal title="New database" wide onClose={onClose}
      sub="Runs on a node with the prod role, on the walroos network: apps on that node reach it by name. No ports open on the node."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !f.name || !f.version} onClick={submit}>{busy ? 'Creating…' : 'Create database'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <div className="field">
          <span className="label" id="engine-h">Engine</span>
          <div className="radio-cards radio-cards-2" role="radiogroup" aria-labelledby="engine-h">
            {engines.map(e => (
              <label key={e.id} className="radio-card">
                <span className="rc-h"><input type="radio" name="engine" value={e.id} aria-label={e.label} checked={f.engine === e.id} onChange={() => pick(e.id)} />
                  <Icon n="database" />{e.label}</span>
                <span className="help">{e.id === 'redis' ? 'Cache and queues. RDB snapshots.' : `SQL on port ${e.port}.`}</span>
              </label>
            ))}
          </div>
        </div>
        <div className="field-row">
          <Field label="Name" hint={<>DNS label. Apps reach it at <span className="mono">walroos-db-{f.name || 'name'}</span>.</>}>
            <input className="input mono" value={f.name} onChange={e => setF(x => ({ ...x, name: e.target.value }))} placeholder="shop-db" autoFocus />
          </Field>
          <Field label="Version">
            <select className="input mono" value={f.version} onChange={e => setF(x => ({ ...x, version: e.target.value }))} disabled={!engine}>
              {engine?.versions.map(v => <option key={v} value={v}>{v}</option>)}
            </select>
          </Field>
        </div>
        <Field label="Node" hint={prod.length === 0 ? <span style={{ color: 'var(--bad)' }}>No node with the prod role. Join one with <span className="mono">--roles prod</span>.</span>
          : 'Put it on the node of the apps that use it: they only reach databases on their own node.'}>
          <select className="input" value={f.node} onChange={e => setF(x => ({ ...x, node: e.target.value }))}>
            <option value="">Auto (first online)</option>
            {prod.map(n => <option key={n.id} value={n.name} disabled={n.status !== 'online'}>{n.name}{n.status !== 'online' ? ` (${n.status})` : ''}</option>)}
          </select>
        </Field>
      </div>
    </Modal>
  );
}

/* ---------- detail ---------- */
export function DatabaseDetail({ name }: { name: string }) {
  const toast = useToast();
  const [d, setD] = useState<Database | null>(null);
  const [backups, setBackups] = useState<Backup[] | null>(null);
  const [error, setError] = useState('');
  const [reveal, setReveal] = useState(false);
  const [linking, setLinking] = useState(false);
  const [scheduling, setScheduling] = useState(false);
  const [restoring, setRestoring] = useState<Backup | null>(null);
  const [removing, setRemoving] = useState(false);
  const [purge, setPurge] = useState(false);
  const [backingUp, setBackingUp] = useState(false);

  const load = useCallback(() => {
    api.database(name).then(x => { setD(x); setError(''); }, e => setError(e.message));
    api.backups(name).then(setBackups, () => setBackups([]));
  }, [name]);
  usePoll(load, d?.state === 'activating' ? 2000 : 15000);

  if (error && !d) {
    return <EmptyState icon="alert" title={error === 'database not found' ? 'No such database' : "Couldn't load the database"} text={error}
      action={<a className="btn" href="#/databases">Back to databases</a>} />;
  }
  if (!d) return <div className="skel-list" aria-busy="true" aria-label="Loading"><div className="skel-row"><i className="skel w30" /><i className="skel w50" /></div></div>;

  const [s, l] = dbState(d);
  const url = d.url ?? '';
  const masked = url.replace(/:([^:@/]+)@/, ':••••••••@');
  const backupNow = () => {
    setBackingUp(true);
    api.backupNow(d.name).then(b => { toast(`Backed up: ${b.file}`); load(); }, e => { toast(e.message, 'bad'); load(); })
      .finally(() => setBackingUp(false));
  };
  const remove = () => api.deleteDatabase(d.name, purge).then(
    () => { toast(`${d.name} deleted`); location.hash = '#/databases'; },
    e => { setRemoving(false); toast(e.message, 'bad'); },
  );

  return (
    <>
      <div className="page-head">
        <div className="ph-text">
          <div className="title-row"><h1 className="mono">{d.name}</h1><Badge s={s} label={l} /></div>
          <div className="ph-meta">
            <span><Icon n="database" />{label(d)}</span>
            <span><Icon n="nodes" /><span className="mono">{d.node_name}</span></span>
          </div>
        </div>
        <div className="ph-actions">
          <button className="btn btn-primary" disabled={d.node_down || backingUp} onClick={backupNow}><Icon n="upload" />{backingUp ? 'Backing up…' : 'Back up now'}</button>
          <button className="btn" onClick={() => setLinking(true)}><Icon n="plug" />Link to app</button>
          <button className="btn-icon" aria-label={`Delete ${d.name}`} title="Delete" onClick={() => setRemoving(true)}><Icon n="trash" /></button>
        </div>
      </div>

      {d.node_down && (
        <div className="alert alert-bad" role="alert"><Icon n="alert" />
          <div className="alert-text"><strong>{d.node_name} isn't answering</strong><span className="muted">Backups wait for it. The database keeps running if the node is up but the hub can't reach it.</span></div>
        </div>
      )}
      {d.state === 'missing' && (
        <div className="alert alert-bad" role="alert"><Icon n="alert" />
          <div className="alert-text"><strong>{d.node_name} doesn't have it</strong><span className="muted">Its unit was removed on the node. Delete it here and create it again; the data volume is still there unless it was purged.</span></div>
        </div>
      )}

      <section className="card" aria-labelledby="conn-h">
        <div className="card-head"><div><h2 id="conn-h">Connection</h2>
          <p className="muted">From apps on {d.node_name}, over the walroos network. Nothing outside the node can reach it.</p></div></div>
        <div className="card-body">
          <dl className="kv">
            <dt>Host</dt><dd className="mono">{d.host}</dd>
            <dt>Port</dt><dd className="mono">{d.port}</dd>
            {d.engine !== 'redis' && <><dt>User</dt><dd className="mono">{d.user}</dd><dt>Database</dt><dd className="mono">{d.db}</dd></>}
          </dl>
          {url && (
            <div className="cmd" style={{ marginTop: 'var(--sp3)' }}>
              <code className="od-truncate" title={reveal ? url : undefined}>{reveal ? url : masked}</code>
              <button className="btn-icon" aria-label={reveal ? 'Hide password' : 'Show password'} title={reveal ? 'Hide' : 'Show'} onClick={() => setReveal(!reveal)}><Icon n="eye" /></button>
              <CopyBtn text={url} label="Copy connection URL" />
            </div>
          )}
          <p className="help" style={{ marginTop: 'var(--sp2)' }}><strong>Link to app</strong> puts this URL in an app's environment as a secret; it applies on the app's next deploy.</p>
        </div>
      </section>

      <section className="card" aria-labelledby="bk-h">
        <div className="card-head">
          <div><h2 id="bk-h">Backups</h2>
            <p className="muted">Gzipped dumps on {d.node_name}, in <span className="mono">~/walroos-backups/{d.name}</span>. {schedule(d) === 'off' ? 'Scheduled backups are off.' : `Scheduled ${schedule(d)}, keeping ${d.backup_keep}.`}</p></div>
          <button className="btn" onClick={() => setScheduling(true)}><Icon n="settings" />Schedule</button>
        </div>
        <div className="card-body">
          {d.last_backup_error && <ErrorAlert title={`The last backup failed (${ago(d.last_backup_at ?? null)})`} msg={d.last_backup_error} />}
          {!backups ? <p className="muted">Loading…</p> : backups.length === 0 ? <p className="muted">No backups yet. <strong>Back up now</strong> makes the first one.</p> : (
            <div className="table-wrap">
              <table className="tbl">
                <thead><tr><th className="col-grow">File</th><th className="hide-xs">Size</th><th className="hide-sm">Taken</th><th><span className="sr-only">Actions</span></th></tr></thead>
                <tbody>
                  {backups.map(b => (
                    <tr key={b.file}>
                      <td className="col-grow"><div className="cell-stack"><span className="cell-title mono" title={b.file}>{b.file}</span>
                        <span className="cell-sub show-xs-inline">{size(b.size)} · {ago(b.created_at)}</span></div></td>
                      <td className="hide-xs mono od-nowrap">{size(b.size)}</td>
                      <td className="hide-sm muted od-nowrap">{ago(b.created_at)}</td>
                      <td className="ta-r od-nowrap">
                        <a className="btn-icon" href={api.backupURL(d.name, b.file)} download={b.file} aria-label={`Download ${b.file}`} title="Download"><Icon n="download" /></a>
                        <button className="btn-icon" aria-label={`Restore ${b.file}`} title="Restore" disabled={d.node_down} onClick={() => setRestoring(b)}><Icon n="rollback" /></button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <p className="help" style={{ marginTop: 'var(--sp2)' }}>They live on the same disk as the database: download one now and then to keep a copy elsewhere.</p>
        </div>
      </section>

      {linking && <LinkModal d={d} onClose={() => setLinking(false)} />}
      {scheduling && <ScheduleModal d={d} onClose={() => setScheduling(false)} onSaved={x => { setScheduling(false); setD(x); }} />}
      {restoring && (
        <Confirm title={`Restore ${d.name}?`} confirmLabel="Restore" danger onClose={() => setRestoring(null)}
          onConfirm={() => {
            const b = restoring; setRestoring(null);
            toast(`Restoring ${b.file}…`);
            api.restore(d.name, b.file).then(() => { toast(`${d.name} restored from ${b.file}`); load(); }, e => toast(e.message, 'bad'));
          }}>
          <p className="muted">Replaces what's in <span className="mono">{d.name}</span> with the dump from {ago(restoring.created_at)} (<span className="mono">{restoring.file}</span>). Anything written since is lost. Back up now first if you may want it back.</p>
        </Confirm>
      )}
      {removing && (
        <Confirm title={`Delete ${d.name}?`} confirmLabel="Delete database" danger onClose={() => setRemoving(false)} onConfirm={remove}>
          <p className="muted">Stops it on <span className="mono">{d.node_name}</span> and forgets it here. Apps using it lose their connection.</p>
          <label className="check" style={{ marginTop: 'var(--sp3)' }}>
            <input type="checkbox" checked={purge} onChange={e => setPurge(e.target.checked)} />
            Also delete its data and its backups. Otherwise a database created again as {d.name} finds its data, but keeps its old password.
          </label>
        </Confirm>
      )}
    </>
  );
}

function LinkModal({ d, onClose }: { d: Database; onClose: () => void }) {
  const toast = useToast();
  const [apps, setApps] = useState<App[] | null>(null);
  const [app, setApp] = useState('');
  const [key, setKey] = useState(d.engine === 'redis' ? 'REDIS_URL' : 'DATABASE_URL');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.apps().then(list => { const here = list.filter(a => a.node_id === d.node_id); setApps(here); setApp(here[0]?.name ?? ''); }, e => setError(e.message));
  }, [d.node_id]);
  const submit = () => {
    setBusy(true);
    api.linkDatabase(d.name, app, key).then(() => { toast(`${key} set on ${app}: deploy it to apply`); onClose(); },
      e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title={`Link ${d.name} to an app`} onClose={onClose}
      sub="Adds the connection URL to the app's environment as a secret variable. It applies on the app's next deploy."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !app || !key} onClick={submit}>{busy ? 'Linking…' : 'Link'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't link it" msg={error} />}
      <div className="form">
        <Field label="App" hint={apps && apps.length === 0 ? `No app runs on ${d.node_name}. Apps only reach databases on their own node.` : `Apps on ${d.node_name}.`}>
          <select className="input" value={app} onChange={e => setApp(e.target.value)} disabled={!apps?.length}>
            {apps?.map(a => <option key={a.id} value={a.name}>{a.name}</option>)}
          </select>
        </Field>
        <Field label="Variable" hint="Replaces a variable with that name if the app has one.">
          <input className="input mono" value={key} onChange={e => setKey(e.target.value.toUpperCase())} />
        </Field>
      </div>
    </Modal>
  );
}

function ScheduleModal({ d, onClose, onSaved }: { d: Database; onClose: () => void; onSaved: (d: Database) => void }) {
  const [every, setEvery] = useState(String(d.backup_every_hours));
  const [keep, setKeep] = useState(String(d.backup_keep));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    api.setSchedule(d.name, Number(every), Number(keep)).then(onSaved, e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title={`Backups · ${d.name}`} onClose={onClose} sub="The hub starts a backup when the last one is older than the interval. Older dumps beyond the count are removed."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy} onClick={submit}>{busy ? 'Saving…' : 'Save'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't save it" msg={error} />}
      <div className="form">
        <div className="field-row">
          <Field label="Every" hint="0 turns scheduled backups off.">
            <select className="input" value={every} onChange={e => setEvery(e.target.value)}>
              {[[0, 'Off'], [1, 'Hour'], [6, '6 hours'], [12, '12 hours'], [24, 'Day'], [168, 'Week']].map(([v, l]) => <option key={v} value={v}>{l}</option>)}
              {![0, 1, 6, 12, 24, 168].includes(d.backup_every_hours) && <option value={d.backup_every_hours}>{d.backup_every_hours} hours</option>}
            </select>
          </Field>
          <Field label="Keep" hint="Dumps kept on the node, 1 to 100.">
            <input className="input mono" value={keep} onChange={e => setKeep(e.target.value)} inputMode="numeric" />
          </Field>
        </div>
      </div>
    </Modal>
  );
}
