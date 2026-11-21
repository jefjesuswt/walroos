// The dialogs of an app's page, and the one that creates an app.
import { useEffect, useState } from 'react';
import { api, type App, type Cloudflare, type Deployment, type Volume } from '../api';
import { Alert, ErrorAlert, Field, Foot, Modal, NodeSelect, ProjectField, useToast } from '../ui';

// Compose-like: name:/target for a podman volume, ./dir:/target for a directory under the
// node's ~/walroos-data; :ro at the end mounts it read-only.
export const volumeLine = (v: Volume) => `${v.name ?? `./${v.host_path}`}:${v.target}${v.ro ? ':ro' : ''}`;
export const parseVolume = (line: string): Volume => {
  const [src = '', target = '', opt] = line.split(':');
  const v: Volume = src.startsWith('./') ? { host_path: src.slice(2), target } : { name: src, target };
  return opt === 'ro' ? { ...v, ro: true } : v;
};

export function VolumesModal({ app, onClose, onSaved }: { app: App; onClose: () => void; onSaved: () => void }) {
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
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} icon="check" label="Save" busyLabel="Saving…" />}>
      {error && <ErrorAlert title="Couldn't save them" msg={error} />}
      <div className="form">
        <Field label="One per line" hint={<><span className="mono">name:/path</span> is a podman volume; <span className="mono">./dir:/path</span> is a directory in <span className="mono">~/walroos-data</span> on {app.node_name} (made if missing). Add <span className="mono">:ro</span> for read-only.</>}>
          <textarea className="input mono" rows={4} value={text} onChange={e => setText(e.target.value)} placeholder={'data:/var/lib/data\n./photos:/photos:ro'} spellCheck={false} autoFocus />
        </Field>
      </div>
    </Modal>
  );
}

export function DomainsModal({ app, onClose, onSaved }: { app: App; onClose: () => void; onSaved: () => void }) {
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
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={cf?.configured === false} icon="check" label="Save" busyLabel="Saving…" />}>
      {cf?.configured === false && <Alert tone="busy" title="Cloudflare isn't set up">Add an API token in <a href="#/settings">Settings</a> first.</Alert>}
      {error && <ErrorAlert title="Couldn't save them" msg={error} />}
      <div className="form">
        <Field label="Hostnames, one per line" hint={cf?.zones.length ? <>In a zone the token can edit: <span className="mono">{cf.zones.join(', ')}</span>. Empty removes them all.</> : 'Empty removes them all.'}>
          <textarea className="input mono" rows={4} value={text} onChange={e => setText(e.target.value)} placeholder={`${app.name}.example.com`} spellCheck={false} autoFocus />
        </Field>
      </div>
    </Modal>
  );
}

export function DeployModal({ app, onClose, onQueued }: { app: App; onClose: () => void; onQueued: (d: Deployment) => void }) {
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
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} icon="deploy" label="Deploy" busyLabel="Queuing…" />}>
      {error && <ErrorAlert title="Couldn't queue it" msg={error} />}
      {!app.image && <div className="form">
        <Field label="Commit" optional hint={<>Empty deploys the head of <span className="mono">{app.branch || 'the default branch'}</span>. A SHA (7 to 40 hex) pins a commit.</>}>
          <input className="input mono" value={sha} onChange={e => setSha(e.target.value)} placeholder="branch head" autoFocus
            onKeyDown={e => e.key === 'Enter' && !busy && submit()} />
        </Field>
      </div>}
    </Modal>
  );
}

export function NewAppModal({ onClose, onCreated, project = '' }: { onClose: () => void; onCreated: (a: App) => void; project?: string }) {
  const toast = useToast();
  const [f, setF] = useState({ name: '', description: '', node: '', project });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    api.createApp({ name: f.name, description: f.description, node: f.node, project: f.project, port: 0 })
      .then(a => { toast(`${a.name} created: set its source`); onCreated(a); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title="Create application" onClose={onClose} sub="Give it a name. Its source (a Git repository or a Docker image) goes in its General tab."
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!f.name} label="Create" busyLabel="Creating…" />}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <Field label="Name" hint={<>DNS label. Other services of the project reach it as <span className="mono">{f.name || 'name'}:port</span>.</>}>
          <input className="input mono" value={f.name} onChange={e => setF(x => ({ ...x, name: e.target.value }))} placeholder="api" autoFocus />
        </Field>
        <Field label="Description" optional>
          <input className="input" value={f.description} onChange={e => setF(x => ({ ...x, description: e.target.value }))} placeholder="The backend" />
        </Field>
        <ProjectField value={f.project} onChange={v => setF(x => ({ ...x, project: v }))} locked={!!project} />
        <NodeSelect role="prod" value={f.node} onChange={node => setF(x => ({ ...x, node }))} />
      </div>
    </Modal>
  );
}

