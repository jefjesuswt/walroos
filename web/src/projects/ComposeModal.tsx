// Compose: a docker-compose.yml, from a repo or pasted, as the project's apps. Preview first (what
// it creates, updates and removes), then sync: it saves the source and deploys every service.
import { useEffect, useState } from 'react';
import { api, type ComposeDiff, type ComposeSource } from '../api';
import { Alert, BrandLogo, ErrorAlert, Field, Foot, FormRow, Modal, Seg, useToast } from '../ui';

export function ComposeModal({ project, onClose, onSynced }: { project: string; onClose: () => void; onSynced: () => void }) {
  const toast = useToast();
  const [fromRepo, setFromRepo] = useState(true);
  const [src, setSrc] = useState<ComposeSource>({ repo_url: '', branch: '', path: '', text: '' });
  const [plan, setPlan] = useState<ComposeDiff | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.composeSource(project).then(s => {
      if (s.repo_url || s.text) { setSrc({ ...src, ...s }); setFromRepo(!!s.repo_url); }
    }, () => undefined);
  }, [project]);

  const source = (): ComposeSource => fromRepo ? { repo_url: src.repo_url?.trim(), branch: src.branch?.trim(), path: src.path?.trim() } : { text: src.text };
  const set = (k: keyof ComposeSource) => (e: { target: { value: string } }) => { setSrc(x => ({ ...x, [k]: e.target.value })); setPlan(null); };
  const run = (fn: () => Promise<unknown>) => { setBusy(true); setError(''); fn().catch(e => setError(e.message)).finally(() => setBusy(false)); };
  const preview = () => run(() => api.planCompose(project, source()).then(setPlan));
  const sync = () => run(() => api.syncCompose(project, source()).then(d => {
    toast(`Synced: deploying ${d.deployments?.length ?? 0} service(s)`); onSynced();
  }));

  return (
    <Modal title={`Compose · ${project}`} wide onClose={onClose}
      sub="Each service becomes an app of this project, named project-service, and the others reach it by its service name."
      foot={<Foot onCancel={onClose} busy={busy} disabled={fromRepo ? !src.repo_url : !src.text}
        label={plan ? 'Sync and deploy' : 'Preview'} busyLabel={plan ? 'Syncing…' : 'Reading…'} icon={plan ? 'deploy' : 'eye'} onClick={plan ? sync : preview} />}>
      {error && <ErrorAlert title="The compose file can't be used" msg={error} />}
      <div className="form">
        <Seg label="Source" value={fromRepo} onChange={v => { setFromRepo(v); setPlan(null); }}
          options={[[true, <><BrandLogo name="git" size={16} />From a repo</>], [false, 'Paste it']]} />
        {fromRepo ? <>
          <Field label="Repository" hint="Its build: contexts build from it, and each sync reads the file again. A private GitHub repo uses the token in Settings.">
            <input className="input mono" value={src.repo_url} onChange={set('repo_url')} placeholder="https://github.com/me/shop.git" autoFocus />
          </Field>
          <FormRow>
            <Field label="Branch" optional><input className="input mono" value={src.branch} onChange={set('branch')} placeholder="default branch" /></Field>
            <Field label="Compose file" optional><input className="input mono" value={src.path} onChange={set('path')} placeholder="docker-compose.yml" /></Field>
          </FormRow>
        </> : (
          <Field label="docker-compose.yml" hint="Published images only: a pasted file has nothing to build from.">
            <textarea className="input mono" rows={8} value={src.text} onChange={set('text')} spellCheck={false} placeholder={'services:\n  db:\n    image: postgres:18'} />
          </Field>
        )}
        <p className="help">{'${VAR}'} comes from the project's Variables (and the repo's .env). Not supported: bind mounts, build args, entrypoint, privileged and the like; it says which.</p>
        {plan && <ComposePlan plan={plan} />}
      </div>
    </Modal>
  );
}

function ComposePlan({ plan }: { plan: ComposeDiff }) {
  const verb = (app: string) => plan.create.includes(app) ? 'create' : 'update';
  return <>
    <ol className="compose-plan">
      {plan.services.map(s => (
        <li key={s.app}>
          <span className={`chip compose-${verb(s.app)}`}>{verb(s.app)}</span>
          <span className="mono">{s.app}</span>
          <span className="muted od-truncate">{s.source.image || `build ${s.source.context || '.'}`}
            {s.source.port ? ` · port ${s.source.port}` : ' · no port'}{s.volumes.length ? ` · ${s.volumes.length} volume(s)` : ''}</span>
        </li>
      ))}
      {plan.remove.map(a => <li key={a}><span className="chip compose-remove">remove</span><span className="mono">{a}</span><span className="muted">keeps its volumes</span></li>)}
    </ol>
    {plan.warnings.length > 0 && <Alert tone="busy" title="Ignored">{plan.warnings.join('. ')}.</Alert>}
  </>;
}
