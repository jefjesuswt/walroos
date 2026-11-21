// The integrations whose API tokens the hub keeps (sealed): Cloudflare for domains, GitHub for
// new projects' repos.
import { useCallback, useState, type ReactNode } from 'react';
import { api, type Cloudflare, type GitHub } from '../api';
import { Alert, Badge, Confirm, Field, Icon, Section, usePoll, useToast } from '../ui';

// TokenForm is the password field + Save that both cards use. The token is checked by the hub
// against the service before it's stored, and never shown again.
function TokenForm({ label, hint, onSave }: { label: string; hint: ReactNode; onSave: (token: string) => Promise<unknown> }) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const save = () => {
    setBusy(true);
    onSave(token).then(() => setToken(''), () => undefined).finally(() => setBusy(false));
  };
  return (
    <div className="form">
      <Field label={label} hint={hint}>
        <input className="input mono" type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && token && !busy && save()} />
      </Field>
      <div><button className="btn btn-primary" disabled={!token || busy} onClick={save}><Icon n="check" />{busy ? 'Checking…' : 'Save'}</button></div>
    </div>
  );
}

const connected = (ok: boolean) => <Badge s={ok ? 'ok' : 'idle'} label={ok ? 'Connected' : 'Not set up'} />;

export function CloudflareCard() {
  const toast = useToast();
  const [cf, setCf] = useState<Cloudflare | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(() => { api.cloudflare().then(c => { setCf(c); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 60000);
  const save = (token: string) => api.setCloudflare(token).then(
    c => { setCf(c); setError(''); toast('Cloudflare token saved'); },
    e => { setError(e.message); throw e; },
  );
  return (
    <Section id="cf-h" title="Cloudflare" actions={cf && connected(cf.configured)}
      sub="Apps get public domains through a Cloudflare Tunnel per prod node: TLS at Cloudflare's edge, no open ports, works behind CGNAT.">
      {error && <Alert title="Cloudflare said no">{error}</Alert>}
      {cf?.configured && <p>Zones: <span className="mono">{cf.zones.join(', ') || '—'}</span></p>}
      <TokenForm label={cf?.configured ? 'Replace the API token' : 'API token'} onSave={save}
        hint={<>Create one at dash.cloudflare.com → My Profile → API Tokens with <span className="mono">Zone · DNS · Edit</span>, <span className="mono">Zone · Zone · Read</span> and <span className="mono">Account · Cloudflare Tunnel · Edit</span>. It's stored encrypted and never shown again.</>} />
    </Section>
  );
}

export function GitHubCard() {
  const toast = useToast();
  const [gh, setGh] = useState<GitHub | null>(null);
  const [error, setError] = useState('');
  const [removing, setRemoving] = useState(false);
  const load = useCallback(() => { api.github().then(g => { setGh(g); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, 60000);
  const save = (token: string) => api.setGitHub(token).then(
    g => { setGh(g); setError(''); toast(`GitHub token saved for ${g.login}`); },
    e => { setError(e.message); throw e; },
  );
  const remove = () => api.removeGitHub().then(() => { setRemoving(false); toast('GitHub token removed'); load(); }, e => toast(e.message, 'bad'));
  return (
    <Section id="gh-h" title="GitHub" actions={gh && connected(gh.configured)}
      sub="New projects get their repo created on your GitHub account, with a first commit holding the devcontainer and a README.">
      {error && <Alert title="GitHub said no">{error}</Alert>}
      {gh?.configured && (
        <div className="section-head">
          <p>Repos are created as <span className="mono">{gh.login}</span>.</p>
          <button className="btn btn-ghost" onClick={() => setRemoving(true)}><Icon n="trash" />Remove</button>
        </div>
      )}
      <TokenForm label={gh?.configured ? 'Replace the token' : 'Personal access token'} onSave={save}
        hint={<>Create one at github.com → Settings → Developer settings: a classic token with the <span className="mono">repo</span> scope, or a fine-grained one with <span className="mono">Administration</span> and <span className="mono">Contents</span> read and write. It's stored encrypted and never shown again.</>} />
      {removing && (
        <Confirm title="Remove the GitHub token?" confirmLabel="Remove token" danger onClose={() => setRemoving(false)} onConfirm={remove}>
          <p className="muted">New projects will start as local git repos until you add a token again. Repos already created stay on GitHub; revoke the token there too if you're done with it.</p>
        </Confirm>
      )}
    </Section>
  );
}
