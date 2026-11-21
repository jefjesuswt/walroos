// The Automation tab: how the app deploys by itself. On push, the forge's webhook deploys every
// push to the branch; after CI, the forge's workflow runs the tests and calls the hub with a
// deploy token once they pass. The tests always run in the forge: walroos only deploys.
import { useEffect, useState } from 'react';
import { api, type App, type Hooks, type Trigger } from '../api';
import { Alert, CodeBlock, Confirm, CopyField, Field, Icon, Note, Section, Seg, useToast } from '../ui';
import { githubWorkflow } from './workflow';

type Forge = 'github' | 'gitea' | 'gitlab';
const FORGES: [Forge, string][] = [['github', 'GitHub'], ['gitea', 'Gitea · Forgejo'], ['gitlab', 'GitLab']];
const TRIGGERS: [Trigger, string][] = [['manual', 'Manual'], ['on_push', 'On push'], ['on_ci', 'After CI']];

export function AutomationTab({ app, canEdit, onChange }: { app: App; canEdit: boolean; onChange: (a: App) => void }) {
  const toast = useToast();
  const [hooks, setHooks] = useState<Hooks | null>(null);
  useEffect(() => { if (canEdit) api.hooks().then(setHooks, () => undefined); }, [canEdit]);
  if (!canEdit) return <Note>Your role can't change how this app deploys.</Note>;

  const setTrigger = (t: Trigger) => api.setTrigger(app.name, t).then(
    a => { onChange(a); toast(t === 'manual' ? 'Deploys are manual now' : 'Automatic deploys on'); },
    e => toast(e.message, 'bad'),
  );
  const branch = app.image ? '' : app.branch || 'the default branch';
  return <>
    <Section id="auto-h" title="Deploys" sub="Tests run in your forge (GitHub Actions, Gitea Actions, GitLab CI); walroos deploys.">
      <Seg label="How it deploys" value={app.trigger} onChange={setTrigger} options={TRIGGERS} />
      <p className="help">{app.trigger === 'manual' ? 'Only the Deploy button, the API or MCP deploy it.'
        : app.trigger === 'on_push' ? `Every push to ${branch || 'the repo'} deploys that commit.`
        : 'Your workflow calls walroos once its tests pass. Nothing deploys if they fail.'}</p>
      {app.trigger !== 'manual' && hooks?.error && <Alert tone="busy" title="The forge can't reach walroos yet">{hooks.error}</Alert>}
    </Section>
    {app.trigger === 'on_push' && <WebhookCard app={app} url={hooks?.url ?? ''} />}
    {app.trigger === 'on_ci' && <DeployTokenCard app={app} url={hooks?.url ?? ''} onChange={onChange} />}
  </>;
}

function WebhookCard({ app, url }: { app: App; url: string }) {
  const toast = useToast();
  const [forge, setForge] = useState<Forge>('github');
  const [secret, setSecret] = useState('');
  const [rotating, setRotating] = useState(false);
  useEffect(() => { api.webhookSecret(app.name).then(r => setSecret(r.secret), e => toast(e.message, 'bad')); }, [app.name]);
  const rotate = () => api.rotateWebhookSecret(app.name).then(
    r => { setSecret(r.secret); setRotating(false); toast('New secret: update it in the forge'); },
    e => toast(e.message, 'bad'),
  );
  return (
    <Section id="hook-h" title="Webhook" sub="Add it in the repo's settings → Webhooks. Only push events; content type application/json."
      actions={<button className="btn" onClick={() => setRotating(true)}><Icon n="refresh" />New secret</button>}>
      <Seg label="Forge" value={forge} onChange={setForge} options={FORGES} />
      <Field label="Payload URL">
        <CopyField value={`${url || '<hub URL>'}/hooks/${forge}/${app.name}`} label="payload URL" />
      </Field>
      <Field label={forge === 'gitlab' ? 'Secret token' : 'Secret'} hint={forge === 'github' ? 'GitHub signs each delivery with it (X-Hub-Signature-256).' : undefined}>
        {secret ? <CopyField value={secret} masked="••••••••••••••••" label="webhook secret" /> : <p className="muted">Loading…</p>}
      </Field>
      {rotating && (
        <Confirm title="Make a new secret?" confirmLabel="New secret" onClose={() => setRotating(false)} onConfirm={rotate}>
          <p className="muted">Pushes signed with the old one stop deploying until you paste the new one in the forge.</p>
        </Confirm>
      )}
    </Section>
  );
}

function DeployTokenCard({ app, url, onChange }: { app: App; url: string; onChange: (a: App) => void }) {
  const toast = useToast();
  const [token, setToken] = useState('');
  const [revoking, setRevoking] = useState(false);
  const refresh = () => api.app(app.name).then(onChange, () => undefined);
  const make = () => api.newDeployToken(app.name).then(r => { setToken(r.token); refresh(); }, e => toast(e.message, 'bad'));
  const revoke = () => api.revokeDeployToken(app.name).then(
    () => { setToken(''); setRevoking(false); refresh(); toast('Deploy token revoked'); },
    e => toast(e.message, 'bad'),
  );
  return <>
    <Section id="token-h" title="Deploy token" sub="Your workflow sends it to deploy this app, and only this app. Store it as a repository secret."
      actions={app.deploy_token && <button className="btn btn-ghost" onClick={() => setRevoking(true)}><Icon n="trash" />Revoke</button>}>
      {token ? <>
        <Alert tone="busy" title="Copy it now">It's shown this once. Save it as WALROOS_DEPLOY_TOKEN in the repo's secrets.</Alert>
        <CopyField value={token} label="deploy token" />
      </> : (
        <div className="actions">
          <button className="btn btn-primary" onClick={make}><Icon n="plus" />{app.deploy_token ? 'Replace the token' : 'Create a token'}</button>
          {app.deploy_token && <span className="muted">A token is set. Replacing it stops the old one.</span>}
        </div>
      )}
      <Field label="WALROOS_URL" hint="Save it as a repository secret too (or a variable).">
        <CopyField value={url || '<hub URL>'} label="hooks URL" />
      </Field>
    </Section>
    <Section id="wf-h" title="GitHub Actions workflow"
      sub={app.image ? 'Tests, then builds and pushes the image, then deploys it. Save as .github/workflows/deploy.yml.' : 'Tests, then deploys the commit that passed. Save as .github/workflows/deploy.yml.'}>
      <CodeBlock code={githubWorkflow(app)} label="workflow" />
      <p className="help">Replace the test step with yours. Gitea and Forgejo Actions run the same file; on GitLab, call the same URL from the last stage.</p>
    </Section>
    {revoking && (
      <Confirm title="Revoke the deploy token?" confirmLabel="Revoke" danger onClose={() => setRevoking(false)} onConfirm={revoke}>
        <p className="muted">Workflows using it can't deploy {app.name} until you make a new one.</p>
      </Confirm>
    )}
  </>;
}
