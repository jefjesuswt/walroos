// Pickers and editors that several dialogs share: which node, which project, the variables.
import { useEffect, useState } from 'react';
import { api, type EnvVar, type Node, type Project } from '../api';
import { ErrorAlert, Loading, useToast } from './feedback';
import { Check, Field, Select } from './form';
import { Icon } from './icons';
import { Modal } from './overlay';

// NodeSelect picks the node a new workspace (role dev) or service (role prod) goes to. "" lets
// the hub choose. Dev lists only online nodes (the hub places by load); prod lists them all and
// greys out the offline ones, so you see why yours isn't there.
export function NodeSelect({ role, value, onChange, nodes: given }: {
  role: 'dev' | 'prod'; value: string; onChange: (v: string) => void; nodes?: Node[];
}) {
  const [fetched, setFetched] = useState<Node[]>([]);
  useEffect(() => { if (!given) api.nodes().then(setFetched, () => undefined); }, [given]);
  const nodes = (given ?? fetched).filter(n => n.roles.includes(role) && (role === 'prod' || n.status === 'online'));
  const none = role === 'dev' ? 'No online node with the dev role.' : <>No node with the prod role. Join one with <span className="mono">--roles prod</span>.</>;
  return (
    <Field label="Node" hint={nodes.length === 0 ? none : undefined} tone="bad">
      <Select value={value} onChange={onChange}>
        <option value="">{role === 'dev' ? 'Auto (least loaded)' : 'Auto (first online)'}</option>
        {nodes.map(n => <option key={n.id} value={n.name} disabled={n.status !== 'online'}>{n.name}{n.status !== 'online' ? ` (${n.status})` : ''}</option>)}
      </Select>
    </Field>
  );
}

// ProjectField picks the project a new service goes in. Locked when the dialog was opened from a
// project's page.
export function ProjectField({ value, onChange, locked }: { value: string; onChange: (v: string) => void; locked?: boolean }) {
  const [list, setList] = useState<Project[]>([]);
  useEffect(() => { if (!locked) api.projects().then(setList, () => undefined); }, [locked]);
  if (locked) return null; // opened from the project's page: it's already chosen
  return (
    <Field label="Project" hint="Services of a project reach each other by name, on their own network. Nothing else can reach them.">
      <Select value={value} onChange={onChange}>
        {list.map(p => <option key={p.id} value={p.name === 'default' ? '' : p.name}>{p.name}</option>)}
      </Select>
    </Field>
  );
}

// EnvModal edits a workspace's or an app's variables; the last save replaces the whole set.
export function EnvModal({ title, sub, savedMsg, load, save, onClose, onSaved }: {
  title: string; sub: string; savedMsg: string; load: () => Promise<EnvVar[]>; save: (vars: EnvVar[]) => Promise<unknown>;
  onClose: () => void; onSaved: () => void;
}) {
  const toast = useToast();
  const [vars, setVars] = useState<EnvVar[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => { load().then(setVars, e => setError(e.message)); }, []); // once: a poll would overwrite what you're typing

  const submit = () => {
    setBusy(true);
    save(vars ?? []).then(
      () => { toast(savedMsg); onSaved(); },
      e => { setError(e.message); setBusy(false); },
    );
  };

  return (
    <Modal title={title} wide onClose={onClose} sub={sub}
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !vars} onClick={submit}>{busy ? 'Saving…' : 'Save'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't load or save" msg={error} />}
      {!vars ? <Loading cols={[50]} /> : <EnvRows vars={vars} setVars={setVars} />}
    </Modal>
  );
}

function EnvRows({ vars, setVars }: { vars: EnvVar[]; setVars: (f: (vs: EnvVar[] | null) => EnvVar[]) => void }) {
  const set = (i: number, patch: Partial<EnvVar>) => setVars(vs => (vs ?? []).map((v, j) => j === i ? { ...v, ...patch } : v));
  return (
    <div className="form">
      {vars.length === 0 && <p className="muted">No variables yet.</p>}
      {vars.map((v, i) => (
        <div key={i} className="env-row">
          <input className="input mono" value={v.key} placeholder="KEY" aria-label="Name"
            onChange={e => set(i, { key: e.target.value.toUpperCase() })} />
          <input className="input mono" value={v.value} placeholder="value" aria-label="Value"
            type={v.secret ? 'password' : 'text'} onChange={e => set(i, { value: e.target.value })} />
          <Check checked={v.secret} onChange={secret => set(i, { secret })} title="Mask this value in the UI">secret</Check>
          <button className="btn-icon" aria-label="Remove" title="Remove" onClick={() => setVars(vs => (vs ?? []).filter((_, j) => j !== i))}><Icon n="trash" /></button>
        </div>
      ))}
      <div>
        <button className="btn" onClick={() => setVars(vs => [...(vs ?? []), { key: '', value: '', secret: false }])}><Icon n="plus" />Add variable</button>
      </div>
      <p className="help">Names: A–Z, 0–9 and _, starting with a letter or _. The last save replaces the whole set.</p>
    </div>
  );
}

// EnvEditor is EnvModal's editor in a page (a service's Environment tab).
export function EnvEditor({ load, save, savedMsg }: { load: () => Promise<EnvVar[]>; save: (vars: EnvVar[]) => Promise<unknown>; savedMsg: string }) {
  const toast = useToast();
  const [vars, setVars] = useState<EnvVar[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { load().then(setVars, e => setError(e.message)); }, []); // once: a poll would overwrite what you're typing
  const submit = () => {
    setBusy(true);
    save(vars ?? []).then(() => { toast(savedMsg); setBusy(false); setError(''); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <>
      {error && <ErrorAlert title="Couldn't load or save" msg={error} />}
      {!vars ? <Loading cols={[50]} /> : <EnvRows vars={vars} setVars={setVars} />}
      <div><button className="btn btn-primary" disabled={busy || !vars} onClick={submit}><Icon n="check" />{busy ? 'Saving…' : 'Save'}</button></div>
    </>
  );
}
