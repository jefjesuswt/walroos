// Creating a database, and the dialogs of its page.
import { useEffect, useState } from 'react';
import { api, type App, type Database, type Engine } from '../api';
import { engineLogo } from '../status';
import { Disclosure, ErrorAlert, Field, Foot, FormRow, Mark, Modal, NodeSelect, ProjectField, Select, useToast } from '../ui';

export function NewDatabaseModal({ onClose, onCreated, project = '' }: { onClose: () => void; onCreated: (d: Database) => void; project?: string }) {
  const toast = useToast();
  const [engines, setEngines] = useState<Engine[]>([]);
  const [f, setF] = useState({ name: '', description: '', engine: 'postgres', version: '', db: '', user: '', password: '', node: '', project });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.engines().then(list => { setEngines(list); setF(x => ({ ...x, version: list.find(e => e.id === x.engine)?.versions[0] ?? '' })); },
      e => setError(e.message));
  }, []);
  const engine = engines.find(e => e.id === f.engine);
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF(x => ({ ...x, [k]: e.target.value }));
  const pick = (id: string) => setF(x => ({ ...x, engine: id, version: engines.find(e => e.id === id)?.versions[0] ?? '' }));
  const ident = (f.name || 'name').replace(/-/g, '_').replace(/^(\d)/, 'db_$1'); // what the hub derives

  const submit = () => {
    setBusy(true);
    api.createDatabase(f).then(d => { toast(`${d.name} is starting`); onCreated(d); }, e => { setError(e.message); setBusy(false); });
  };

  return (
    <Modal title="Create database" wide onClose={onClose}
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!f.name || !f.version} label="Create" busyLabel="Creating…" />}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <div className="tile-pick tile-pick-sm" role="radiogroup" aria-label="Engine">
          {engines.map(e => (
            <label key={e.id}>
              <input type="radio" name="engine" value={e.id} aria-label={e.label} checked={f.engine === e.id} onChange={() => pick(e.id)} />
              <Mark logo={engineLogo(e.id)} icon="database" size={26} />
              <span>{e.label}</span>
            </label>
          ))}
        </div>
        <FormRow>
          <Field label="Name" hint={<>The project reaches it as <span className="mono">{f.name || 'name'}:{engine?.port ?? 5432}</span>.</>}>
            <input className="input mono" value={f.name} onChange={set('name')} placeholder="shop-db" autoFocus />
          </Field>
          <Field label="Version">
            <Select mono value={f.version} onChange={version => setF(x => ({ ...x, version }))} disabled={!engine}>
              {engine?.versions.map(v => <option key={v} value={v}>{engine.label} {v}</option>)}
            </Select>
          </Field>
        </FormRow>
        <ProjectField value={f.project} onChange={v => setF(x => ({ ...x, project: v }))} locked={!!project} />
        <Disclosure summary="More options: description, credentials, node">
            <Field label="Description">
              <input className="input" value={f.description} onChange={set('description')} placeholder="Orders and customers" />
            </Field>
            {!['redis', 'valkey'].includes(f.engine) && (
              <FormRow>
                <Field label="Database name">
                  <input className="input mono" value={f.db} onChange={set('db')} placeholder={ident} />
                </Field>
                <Field label="User">
                  <input className="input mono" value={f.user} onChange={set('user')} placeholder={ident} />
                </Field>
              </FormRow>
            )}
            <FormRow>
              <Field label="Password" hint="Empty generates one. Yours: 16+ letters and digits.">
                <input className="input mono" type="password" autoComplete="new-password" value={f.password} onChange={set('password')} placeholder="generated" />
              </Field>
              <NodeSelect role="prod" value={f.node} onChange={node => setF(x => ({ ...x, node }))} />
            </FormRow>
        </Disclosure>
      </div>
    </Modal>
  );
}

export function LinkModal({ d, onClose }: { d: Database; onClose: () => void }) {
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
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!app || !key} label="Link" busyLabel="Linking…" />}>
      {error && <ErrorAlert title="Couldn't link it" msg={error} />}
      <div className="form">
        <Field label="App" hint={apps && apps.length === 0 ? `No app runs on ${d.node_name}. Apps only reach databases on their own node.` : `Apps on ${d.node_name}.`}>
          <Select value={app} onChange={setApp} disabled={!apps?.length}>
            {apps?.map(a => <option key={a.id} value={a.name}>{a.name}</option>)}
          </Select>
        </Field>
        <Field label="Variable" hint="Replaces a variable with that name if the app has one.">
          <input className="input mono" value={key} onChange={e => setKey(e.target.value.toUpperCase())} />
        </Field>
      </div>
    </Modal>
  );
}

export function ScheduleModal({ d, onClose, onSaved }: { d: Database; onClose: () => void; onSaved: (d: Database) => void }) {
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
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} label="Save" busyLabel="Saving…" />}>
      {error && <ErrorAlert title="Couldn't save it" msg={error} />}
      <div className="form">
        <FormRow>
          <Field label="Every" hint="0 turns scheduled backups off.">
            <Select value={every} onChange={setEvery}>
              {[[0, 'Off'], [1, 'Hour'], [6, '6 hours'], [12, '12 hours'], [24, 'Day'], [168, 'Week']].map(([v, l]) => <option key={v} value={v}>{l}</option>)}
              {![0, 1, 6, 12, 24, 168].includes(d.backup_every_hours) && <option value={d.backup_every_hours}>{d.backup_every_hours} hours</option>}
            </Select>
          </Field>
          <Field label="Keep" hint="Dumps kept on the node, 1 to 100.">
            <input className="input mono" value={keep} onChange={e => setKeep(e.target.value)} inputMode="numeric" />
          </Field>
        </FormRow>
      </div>
    </Modal>
  );
}
