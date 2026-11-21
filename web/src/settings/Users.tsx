import { useCallback, useState } from 'react';
import { api, USER_ROLES, type Member, type UserRole } from '../api';
import { ago, Confirm, ErrorAlert, Field, Foot, Icon, Modal, Section, useCan, usePoll, useToast } from '../ui';

const ROLE_HELP: Record<UserRole, string> = {
  owner: 'Everything: nodes, tokens, users and anyone\'s workspaces.',
  deployer: 'Their workspaces, plus apps and databases.',
  developer: 'Their own workspaces. Sees apps and logs, can\'t deploy.',
};

export function UsersCard() {
  const toast = useToast();
  const { me } = useCan();
  const [list, setList] = useState<Member[] | null>(null);
  const [error, setError] = useState('');
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<Member | null>(null);
  const load = useCallback(() => { api.users().then(l => { setList(l); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 30000);
  const setRole = (m: Member, role: UserRole) => api.setUserRole(m.login, role).then(
    () => { toast(`${m.login} is now ${role}`); load(); }, e => { toast(e.message, 'bad'); load(); });
  const remove = (m: Member) => api.removeUser(m.login).then(
    () => { toast(`${m.login} removed`); setRemoving(null); load(); }, e => { toast(e.message, 'bad'); setRemoving(null); });
  return (
    <Section id="users-h" title="Users"
      sub="Tailscale logins that can use this hub. They must already be on your tailnet (share a node with them or invite them)."
      actions={<button className="btn btn-primary" onClick={() => setAdding(true)}><Icon n="plus" />Add user</button>}>
        {error && <ErrorAlert title="Couldn't load users" msg={error} />}
        {list && (
          <div className="table-wrap">
            <table className="tbl">
              <thead><tr><th className="col-grow">Login</th><th>Role</th><th className="hide-sm">Added</th><th><span className="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {list.map(m => {
                  const self = m.login === me?.login;
                  return (
                    <tr key={m.login}>
                      <td className="col-grow"><div className="cell-stack">
                        <span className="cell-title mono" title={m.login}>{m.login}</span>
                        <span className="cell-sub">{self ? 'you' : ROLE_HELP[m.role]}</span>
                      </div></td>
                      <td>
                        <select className="input input-sm" value={m.role} disabled={self} aria-label={`Role of ${m.login}`}
                          title={self ? "Another owner can change your role" : undefined} onChange={e => setRole(m, e.target.value as UserRole)}>
                          {USER_ROLES.map(r => <option key={r} value={r}>{r}</option>)}
                        </select>
                      </td>
                      <td className="hide-sm muted od-nowrap">{ago(m.created_at)}</td>
                      <td className="ta-r">{!self && <button className="btn-icon" aria-label={`Remove ${m.login}`} title="Remove" onClick={() => setRemoving(m)}><Icon n="trash" /></button>}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      {adding && <AddUserModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); load(); }} />}
      {removing && (
        <Confirm title={`Remove ${removing.login}?`} confirmLabel="Remove user" danger onClose={() => setRemoving(null)} onConfirm={() => remove(removing)}>
          <p className="muted">They can't use the hub anymore and their SSH keys go with them. Someone who still owns workspaces can't be removed until those are deleted.</p>
        </Confirm>
      )}
    </Section>
  );
}

function AddUserModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const toast = useToast();
  const [login, setLogin] = useState('');
  const [role, setRole] = useState<UserRole>('developer');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    api.addUser(login.trim(), role).then(() => { toast(`${login.trim()} added as ${role}`); onAdded(); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title="Add user" onClose={onClose} sub="Their Tailscale login, as the Tailscale admin console shows it."
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!login.includes('@')} label="Add user" busyLabel="Adding…" />}>
      {error && <ErrorAlert title="Couldn't add them" msg={error} />}
      <div className="form">
        <Field label="Login">
          <input className="input mono" value={login} onChange={e => setLogin(e.target.value)} placeholder="friend@github" autoFocus />
        </Field>
        <div className="field">
          <span className="label" id="role-h">Role</span>
          <div className="radio-cards" role="radiogroup" aria-labelledby="role-h">
            {USER_ROLES.map(r => (
              <label key={r} className="radio-card">
                <span className="rc-h"><input type="radio" name="role" value={r} aria-label={r} checked={role === r} onChange={() => setRole(r)} />{r}</span>
                <span className="help">{ROLE_HELP[r]}</span>
              </label>
            ))}
          </div>
        </div>
      </div>
    </Modal>
  );
}
