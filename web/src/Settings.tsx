import { useCallback, useMemo, useState } from 'react';
import { api, USER_ROLES, type Cloudflare, type GitHub, type Member, type SSHKey, type UserRole } from './api';
import { addCustomTheme, allThemes, applyTheme, contrastWarnings, parseTheme, pick, removeCustomTheme, savedPref, type Mode, type Theme } from './theme';
import { ago, Badge, Confirm, EmptyState, ErrorAlert, Field, Icon, Modal, useCan, usePoll, useToast } from './ui';

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

const MODES: [Mode, string][] = [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']];

function CloudflareCard() {
  const toast = useToast();
  const [cf, setCf] = useState<Cloudflare | null>(null);
  const [error, setError] = useState('');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => { api.cloudflare().then(c => { setCf(c); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 60000);
  const save = () => {
    setBusy(true);
    api.setCloudflare(token).then(
      c => { setCf(c); setToken(''); setError(''); setBusy(false); toast('Cloudflare token saved'); },
      e => { setError(e.message); setBusy(false); },
    );
  };
  return (
    <section className="card" aria-labelledby="cf-h">
      <div className="card-head">
        <div>
          <h2 id="cf-h">Cloudflare</h2>
          <p className="muted">Apps get public domains through a Cloudflare Tunnel per prod node: TLS at Cloudflare's edge, no open ports, works behind CGNAT.</p>
        </div>
        {cf && <Badge s={cf.configured ? 'ok' : 'idle'} label={cf.configured ? 'Connected' : 'Not set up'} />}
      </div>
      <div className="card-body">
        {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Cloudflare said no</strong><span className="muted">{error}</span></div></div>}
        {cf?.configured && <p>Zones: <span className="mono">{cf.zones.join(', ') || '—'}</span></p>}
        <div className="form">
          <Field label={cf?.configured ? 'Replace the API token' : 'API token'}
            hint={<>Create one at dash.cloudflare.com → My Profile → API Tokens with <span className="mono">Zone · DNS · Edit</span>, <span className="mono">Zone · Zone · Read</span> and <span className="mono">Account · Cloudflare Tunnel · Edit</span>. It's stored encrypted and never shown again.</>}>
            <input className="input mono" type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && token && !busy && save()} />
          </Field>
          <div><button className="btn btn-primary" disabled={!token || busy} onClick={save}><Icon n="check" />{busy ? 'Checking…' : 'Save'}</button></div>
        </div>
      </div>
    </section>
  );
}

const ROLE_HELP: Record<UserRole, string> = {
  owner: 'Everything: nodes, tokens, users and anyone\'s workspaces.',
  deployer: 'Their workspaces, plus apps and databases.',
  developer: 'Their own workspaces. Sees apps and logs, can\'t deploy.',
};

function UsersCard() {
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
    <section className="card" aria-labelledby="users-h">
      <div className="card-head">
        <div>
          <h2 id="users-h">Users</h2>
          <p className="muted">Tailscale logins that can use this hub. They must already be on your tailnet (share a node with them or invite them).</p>
        </div>
        <button className="btn btn-primary" onClick={() => setAdding(true)}><Icon n="plus" />Add user</button>
      </div>
      <div className="card-body">
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
      </div>
      {adding && <AddUserModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); load(); }} />}
      {removing && (
        <Confirm title={`Remove ${removing.login}?`} confirmLabel="Remove user" danger onClose={() => setRemoving(null)} onConfirm={() => remove(removing)}>
          <p className="muted">They can't use the hub anymore and their SSH keys go with them. Someone who still owns workspaces can't be removed until those are deleted.</p>
        </Confirm>
      )}
    </section>
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
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !login.includes('@')} onClick={submit}>{busy ? 'Adding…' : 'Add user'}</button>
      </>}>
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

function GitHubCard() {
  const toast = useToast();
  const [gh, setGh] = useState<GitHub | null>(null);
  const [error, setError] = useState('');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState(false);
  const load = useCallback(() => { api.github().then(g => { setGh(g); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 60000);
  const save = () => {
    setBusy(true);
    api.setGitHub(token).then(
      g => { setGh(g); setToken(''); setError(''); setBusy(false); toast(`GitHub token saved for ${g.login}`); },
      e => { setError(e.message); setBusy(false); },
    );
  };
  const remove = () => api.removeGitHub().then(() => { setRemoving(false); toast('GitHub token removed'); load(); }, e => toast(e.message, 'bad'));
  return (
    <section className="card" aria-labelledby="gh-h">
      <div className="card-head">
        <div>
          <h2 id="gh-h">GitHub</h2>
          <p className="muted">New projects get their repo created on your GitHub account, with a first commit holding the devcontainer and a README.</p>
        </div>
        {gh && <Badge s={gh.configured ? 'ok' : 'idle'} label={gh.configured ? 'Connected' : 'Not set up'} />}
      </div>
      <div className="card-body">
        {error && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>GitHub said no</strong><span className="muted">{error}</span></div></div>}
        {gh?.configured && (
          <div className="section-head">
            <p>Repos are created as <span className="mono">{gh.login}</span>.</p>
            <button className="btn btn-ghost" onClick={() => setRemoving(true)}><Icon n="trash" />Remove</button>
          </div>
        )}
        <div className="form">
          <Field label={gh?.configured ? 'Replace the token' : 'Personal access token'}
            hint={<>Create one at github.com → Settings → Developer settings: a classic token with the <span className="mono">repo</span> scope, or a fine-grained one with <span className="mono">Administration</span> and <span className="mono">Contents</span> read and write. It's stored encrypted and never shown again.</>}>
            <input className="input mono" type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && token && !busy && save()} />
          </Field>
          <div><button className="btn btn-primary" disabled={!token || busy} onClick={save}><Icon n="check" />{busy ? 'Checking…' : 'Save'}</button></div>
        </div>
      </div>
      {removing && (
        <Confirm title="Remove the GitHub token?" confirmLabel="Remove token" danger onClose={() => setRemoving(false)} onConfirm={remove}>
          <p className="muted">New projects will start as local git repos until you add a token again. Repos already created stay on GitHub; revoke the token there too if you're done with it.</p>
        </Confirm>
      )}
    </section>
  );
}

function AppearanceCard() {
  const toast = useToast();
  const [pref, setPref] = useState(savedPref);
  const [themes, setThemes] = useState(allThemes);
  const [importing, setImporting] = useState(false);

  const choose = (id: string, mode: Mode = pref.mode) => { applyTheme(id, mode); setPref({ id, mode }); };
  const remove = (t: Theme) => {
    removeCustomTheme(t.id);
    setThemes(allThemes());
    if (pref.id === t.id) choose('walroos');
    toast(`Theme "${t.name}" removed`);
  };

  return (
    <section className="card" aria-labelledby="look-h">
      <div className="card-head">
        <div>
          <h2 id="look-h">Appearance</h2>
          <p className="muted">Themes fill Material 3 color roles, so any palette works. Saved in this browser.</p>
        </div>
        <button className="btn" onClick={() => setImporting(true)}><Icon n="upload" />Import theme</button>
      </div>
      <div className="card-body">
        <div className="seg" role="group" aria-label="Light or dark">
          {MODES.map(([m, label]) => (
            <button key={m} aria-pressed={pref.mode === m} onClick={() => choose(pref.id, m)}>
              {pref.mode === m && <Icon n="check" />}{label}
            </button>
          ))}
        </div>
        <div className="swatches">
          {themes.map(t => {
            const { scheme } = pick(t, pref.mode);
            const modes = t.schemes.light && t.schemes.dark ? 'Light and dark' : t.schemes.dark ? 'Dark only' : 'Light only';
            return (
              <div key={t.id} className="swatch-wrap">
                <button className="swatch" aria-pressed={pref.id === t.id} onClick={() => choose(t.id)}
                  style={{ '--sw-bg': scheme.surfaceContainer ?? scheme.surface, '--sw-tx': scheme.onSurface } as React.CSSProperties}>
                  <span className="swatch-chips" aria-hidden="true">
                    {[scheme.primary, scheme.secondaryContainer ?? scheme.primaryContainer, scheme.success, scheme.error].filter(Boolean)
                      .map((c, i) => <i key={i} style={{ background: c }} />)}
                  </span>
                  <span className="swatch-name">{pref.id === t.id && <Icon n="check" />}{t.name}</span>
                  <span className="swatch-sub">{t.custom ? 'Imported · ' : ''}{modes}</span>
                </button>
                {t.custom && <button className="btn-icon" aria-label={`Remove ${t.name}`} title="Remove theme" onClick={() => remove(t)}><Icon n="trash" /></button>}
              </div>
            );
          })}
        </div>
      </div>
      {importing && <ImportThemeModal onClose={() => setImporting(false)}
        onImported={t => { addCustomTheme(t); setThemes(allThemes()); choose(t.id); setImporting(false); toast(`Theme "${t.name}" added`); }} />}
    </section>
  );
}

function ImportThemeModal({ onClose, onImported }: { onClose: () => void; onImported: (t: Theme) => void }) {
  const [name, setName] = useState('');
  const [json, setJson] = useState('');
  const parsed = useMemo(() => {
    if (!json.trim()) return null;
    try { const t = parseTheme(json, name); return { t, warnings: contrastWarnings(t) }; }
    catch (e) { return { error: (e as Error).message }; }
  }, [json, name]);

  const readFile = (f?: File) => f?.text().then(text => { setJson(text); if (!name) setName(f.name.replace(/\.json$/i, '')); });

  return (
    <Modal title="Import theme" wide onClose={onClose}
      sub="Paste or pick the JSON that Material Theme Builder exports. Roles it leaves out are derived from the ones it has."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={!parsed || !('t' in parsed)} onClick={() => parsed && 't' in parsed && parsed.t && onImported(parsed.t)}>Import</button>
      </>}>
      <div className="form">
        <Field label="Name"><input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="Catppuccin Frappé" autoFocus /></Field>
        <Field label="Theme JSON" hint={<>Needs <span className="mono">schemes.light</span> and/or <span className="mono">schemes.dark</span> with at least primary, onPrimary, surface and onSurface. <a className="link" href="https://material-foundation.github.io/material-theme-builder/" target="_blank" rel="noreferrer">Material Theme Builder</a> exports this.</>}>
          <textarea className="input mono" rows={8} value={json} onChange={e => setJson(e.target.value)} placeholder='{"schemes": {"dark": {"primary": "#CBA6F7", "onPrimary": "#11111B", "surface": "#1E1E2E", "onSurface": "#CDD6F4"}}}' />
        </Field>
        <div><label className="btn"><Icon n="upload" />Pick a .json file<input type="file" accept=".json,application/json" hidden onChange={e => readFile(e.target.files?.[0])} /></label></div>
        {parsed && 'error' in parsed && <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Can't use this JSON</strong><span className="muted">{parsed.error}</span></div></div>}
        {parsed && 'warnings' in parsed && parsed.warnings && parsed.warnings.length > 0 && (
          <div className="alert alert-busy"><Icon n="alert" /><div className="alert-text"><strong>Some text may be hard to read</strong>
            <span className="muted">Below WCAG AA (4.5:1): {parsed.warnings.join('; ')}. You can import it anyway.</span></div></div>
        )}
      </div>
    </Modal>
  );
}
