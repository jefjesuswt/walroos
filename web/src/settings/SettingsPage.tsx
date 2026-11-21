import { useCallback, useState } from 'react';
import { api, type SSHKey } from '../api';
import { ago, EmptyState, ErrorAlert, Field, Foot, Icon, Loading, LoadError, Modal, Section, useCan, usePoll, useToast } from '../ui';
import { AppearanceCard } from './Appearance';
import { CloudflareCard, GitHubCard } from './Tokens';
import { UsersCard } from './Users';

export function SettingsPage() {
  const toast = useToast();
  const { can } = useCan();
  const [keys, setKeys] = useState<SSHKey[] | null>(null);
  const [error, setError] = useState('');
  const [adding, setAdding] = useState(false);

  const load = useCallback(() => {
    api.sshKeys().then(k => { setKeys(k); setError(''); }, e => setError(e.message));
  }, []);
  usePoll(load, 10000);

  const remove = (k: SSHKey) =>
    api.removeSSHKey(k.id).then(
      () => { toast(`Key "${k.name}" removed`); load(); },
      e => toast(e.message, 'bad'),
    );

  return (
    <>
      <h1 className="sr-only">Settings</h1>

      <AppearanceCard />

      {can('users') && <UsersCard />}

      {can('settings') && <CloudflareCard />}

      {can('settings') && <GitHubCard />}

      <Section id="keys-h" title="SSH keys" flush
        sub="Public keys that can open a shell in your workspaces. The private key never leaves your machine."
        actions={<button className="btn btn-primary" onClick={() => setAdding(true)}><Icon n="plus" />Add key</button>}>
        {error && !keys ? (
<LoadError what="keys" error={error} onRetry={load} />
        ) : !keys ? (
<Loading />
        ) : keys.length === 0 ? (
          <EmptyState icon="plug" title="No keys yet"
            text="Without a key you can't SSH into a workspace. Add the public half of your usual key."
            action={<button className="btn btn-primary" onClick={() => setAdding(true)}><Icon n="plus" />Add key</button>} />
        ) : (
          <div className="table-wrap">
            <table className="tbl">
              <thead><tr><th className="col-grow">Name</th><th className="hide-xs">Fingerprint</th><th className="hide-sm">Added</th><th><span className="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {keys.map(k => (
                  <tr key={k.id}>
                    <td className="col-grow"><div className="cell-stack">
                      <span className="cell-title" title={k.name}>{k.name}</span>
                      <span className="cell-sub mono" title={k.public_key}>{k.public_key}</span>
                    </div></td>
                    <td className="mono hide-xs"><span className="clip" title={k.fingerprint}>{k.fingerprint}</span></td>
                    <td className="hide-sm muted od-nowrap">{ago(k.created_at)}</td>
                    <td className="ta-r"><button className="btn-icon" aria-label={`Remove ${k.name}`} title="Remove key" onClick={() => remove(k)}><Icon n="trash" /></button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Section>

      {adding && <AddKeyModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); load(); }} />}
    </>
  );
}

function AddKeyModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [pub, setPub] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = () => {
    setBusy(true);
    api.addSSHKey(name, pub.trim())
      .then(k => { toast(`Key "${k.name}" added`); onAdded(); },
        e => { setError(e.message); setBusy(false); });
  };

  return (
    <Modal title="Add SSH key" wide onClose={onClose}
      sub="Paste the public key (the .pub file). The agent matches it when you connect."
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!name || !pub.trim()} label="Add key" busyLabel="Adding…" />}>
      {error && <ErrorAlert title="Couldn't add it" msg={error} />}
      <div className="form">
        <Field label="Name">
          <input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="laptop" autoFocus />
        </Field>
        <Field label="Public key" hint={<>Find it with <span className="mono">cat ~/.ssh/id_ed25519.pub</span>.</>}>
          <textarea className="input mono" rows={4} value={pub} onChange={e => setPub(e.target.value)} placeholder="ssh-ed25519 AAAA… you@laptop" />
        </Field>
      </div>
    </Modal>
  );
}
