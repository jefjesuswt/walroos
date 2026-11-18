import { useCallback, useState } from 'react';
import { api, type SSHKey } from './api';
import { ago, EmptyState, Field, Icon, Modal, usePoll, useToast } from './ui';

export function SettingsPage() {
  const toast = useToast();
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
      <h1>Settings</h1>

      <section className="card" aria-labelledby="keys-h">
        <div className="card-head">
          <div>
            <h2 id="keys-h">SSH keys</h2>
            <p className="muted">Public keys that can open a shell in your workspaces. The private key never leaves your machine.</p>
          </div>
          <button className="btn btn-primary" onClick={() => setAdding(true)}><Icon n="plus" />Add key</button>
        </div>

        {error && !keys ? (
          <div className="state state-err" role="alert"><Icon n="alert" cls="ic-lg" /><h3>Couldn't load keys</h3><p className="muted">{error}</p>
            <button className="btn" onClick={load}><Icon n="refresh" />Retry</button></div>
        ) : !keys ? (
          <div className="skel-list" aria-busy="true" aria-label="Loading"><div className="skel-row"><i className="skel w30" /><i className="skel w50" /></div></div>
        ) : keys.length === 0 ? (
          <EmptyState icon="plug" title="No keys yet"
            text="Without a key you can't SSH into a workspace. Add the public half of your usual key."
            action={<button className="btn btn-primary" onClick={() => setAdding(true)}><Icon n="plus" />Add key</button>} />
        ) : (
          <div className="table-wrap">
            <table className="tbl">
              <thead><tr><th>Name</th><th>Fingerprint</th><th className="hide-sm">Added</th><th><span className="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {keys.map(k => (
                  <tr key={k.id}>
                    <td><div className="cell-stack">
                      <span className="cell-title">{k.name}</span>
                      <span className="cell-sub mono od-truncate" style={{ maxWidth: 340 }} title={k.public_key}>{k.public_key}</span>
                    </div></td>
                    <td className="mono od-truncate" style={{ maxWidth: 220 }} title={k.fingerprint}>{k.fingerprint}</td>
                    <td className="hide-sm muted od-nowrap">{ago(k.created_at)}</td>
                    <td className="ta-r"><button className="btn-icon" aria-label={`Remove ${k.name}`} title="Remove key" onClick={() => remove(k)}><Icon n="trash" /></button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

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
      foot={<><button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !name || !pub.trim()} onClick={submit}>{busy ? 'Adding…' : 'Add key'}</button></>}>
      {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't add it</strong><span className="muted">{error}</span></div></div>}
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
