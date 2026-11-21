import { useState } from 'react';
import { api, type App, type StaticBuild } from '../api';
import { isDraft } from '../status';
import { BrandLogo, ErrorAlert, Field, FormRow, Icon, Section, Seg } from '../ui';
import { StaticFields, StaticSummary } from './StaticFields';

// SourceCard is the General tab: where the app comes from (a repo with a Dockerfile, a static site
// or a published image) and how it's checked. A draft shows the form right away; otherwise a summary with Edit.
export function SourceCard({ app, canEdit, onSaved }: { app: App; canEdit: boolean; onSaved: (a: App) => void }) {
  const [editing, setEditing] = useState(isDraft(app));
  const [fromImage, setFromImage] = useState(!!app.image);
  const [isStatic, setStatic] = useState(!!app.static);
  const [build, setBuild] = useState<StaticBuild>(app.static ?? { spa: true });
  const [f, setF] = useState({ repo_url: app.repo_url, image: app.image, branch: app.branch, dockerfile: app.dockerfile,
    port: app.port ? String(app.port) : '', health_path: app.health_path });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF(x => ({ ...x, [k]: e.target.value }));
  const save = () => {
    setBusy(true);
    const src = fromImage ? { image: f.image.trim(), port: Number(f.port), health_path: f.health_path }
      : isStatic ? { repo_url: f.repo_url.trim(), branch: f.branch, static: build, port: 0, health_path: f.health_path }
      : { repo_url: f.repo_url.trim(), branch: f.branch, dockerfile: f.dockerfile, port: Number(f.port), health_path: f.health_path };
    api.setAppSource(app.name, src).then(a => { setBusy(false); setError(''); setEditing(false); onSaved(a); }, e => { setError(e.message); setBusy(false); });
  };

  if (!editing) {
    return (
      <Section id="src-h" title={app.image ? 'Published image' : app.static ? 'Static site' : 'Repository'}
        sub={app.image ? 'Pulled again on each deploy; a rollback runs the exact image it got then.'
          : app.static ? 'Cloned, built and served by walroos on each deploy: no Dockerfile.' : 'Cloned and built from its Dockerfile on each deploy.'}
        actions={canEdit && <button className="btn" onClick={() => setEditing(true)}><Icon n="settings" />Edit</button>}>
          <dl className="kv">
            {app.image ? <><dt>Image</dt><dd className="mono">{app.image}</dd></> : <>
              <dt>Repository</dt><dd className="mono">{app.repo_url}</dd>
              <dt>Branch</dt><dd className="mono">{app.branch || 'default branch'}</dd>
              {app.static ? <StaticSummary value={app.static} /> : <><dt>Dockerfile</dt><dd className="mono">{app.dockerfile || 'Dockerfile'}</dd></>}
            </>}
            <dt>Port</dt><dd className="mono">{app.port}</dd>
            <dt>Health check</dt><dd className="mono">GET {app.health_path || '/'} → 2xx/3xx within 90 s</dd>
          </dl>
      </Section>
    );
  }
  return (
    <Section id="src-h" title="Source" sub="Where the app comes from. It applies on the next deploy.">
        {error && <ErrorAlert title="Couldn't save it" msg={error} />}
        {!canEdit ? <p className="muted">No source yet. Someone who can deploy apps sets it.</p> : (
          <div className="form">
            <Seg label="Source" value={fromImage} onChange={setFromImage}
              options={[[false, <><BrandLogo name="git" size={16} />Git repository</>], [true, <><BrandLogo name="docker" size={16} />Docker image</>]]} />
            {fromImage ? (
              <Field label="Image" hint="Fully qualified, with its registry.">
                <input key="image" className="input mono" value={f.image} onChange={set('image')} placeholder="ghcr.io/owner/app:latest" autoFocus />
              </Field>
            ) : <>
              <Field label="Repository" hint="The node clones with its own git: public repos, or ones its SSH key can read.">
                <input key="repo" className="input mono" value={f.repo_url} onChange={set('repo_url')} placeholder="https://github.com/me/app.git" autoFocus />
              </Field>
              <Field label="Build" group hint={isStatic ? 'No Dockerfile: walroos installs, builds and serves it, like Netlify.' : undefined}>
                <Seg label="Build" value={isStatic} onChange={setStatic} options={[[false, 'Dockerfile'], [true, 'Static site']]} />
              </Field>
              <FormRow>
                <Field label="Branch" optional>
                  <input className="input mono" value={f.branch} onChange={set('branch')} placeholder="default branch" />
                </Field>
                {!isStatic && <Field label="Dockerfile" optional>
                  <input className="input mono" value={f.dockerfile} onChange={set('dockerfile')} placeholder="Dockerfile" />
                </Field>}
              </FormRow>
              {isStatic && <StaticFields value={build} onChange={setBuild} />}
            </>}
            <FormRow>
              {!(isStatic && !fromImage) && <Field label="Port" hint="What the container listens on.">
                <input className="input mono" value={f.port} onChange={set('port')} placeholder="3000" inputMode="numeric" />
              </Field>}
              <Field label="Health check path" optional hint="Must answer 2xx or 3xx within 90 s.">
                <input className="input mono" value={f.health_path} onChange={set('health_path')} placeholder="/" />
              </Field>
            </FormRow>
            <div className="actions">
              <button className="btn btn-primary" disabled={busy || !(fromImage ? f.image : f.repo_url) || !(isStatic && !fromImage) && !Number(f.port)} onClick={save}><Icon n="check" />{busy ? 'Saving…' : 'Save'}</button>
              {!isDraft(app) && <button className="btn" onClick={() => setEditing(false)}>Cancel</button>}
            </div>
          </div>
        )}
    </Section>
  );
}
