import { useCallback, useMemo, useState } from 'react';
import { api, type Cloudflare, type SSHKey } from './api';
import { addCustomTheme, allThemes, applyTheme, contrastWarnings, parseTheme, pick, removeCustomTheme, savedPref, type Mode, type Theme } from './theme';
import { ago, Badge, EmptyState, Field, Icon, Modal, usePoll, useToast } from './ui';

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
      <h1 className="sr-only">Settings</h1>

      <AppearanceCard />

      <CloudflareCard />

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
