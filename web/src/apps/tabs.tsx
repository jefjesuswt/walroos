// The tabs of an app's page, but General (SourceCard.tsx).
import { useCallback, useEffect, useRef, useState } from 'react';
import { api, type App, type Deployment } from '../api';
import { isDraft } from '../status';
import { DangerZone, EnvEditor, Icon, Note, Section, usePoll } from '../ui';
import { DeploymentsTable } from './DeploymentsTable';
import { RefHints } from './RefHints';
import { volumeLine } from './dialogs';

export function EnvironmentTab({ app, canEdit }: { app: App; canEdit: boolean }) {
  if (!canEdit) return <Note>Your role can't see this app's variables: they may hold secrets.</Note>;
  return (
    <Section id="env-h" title="Environment" form
      sub="Passed to the container as KEY=VALUE from a 0600 file on the node, never in the command line. Encrypted in the hub; they apply on the next deploy.">
      <EnvEditor load={() => api.appEnv(app.name)} save={vars => api.setAppEnv(app.name, vars)} savedMsg="Variables saved: deploy to apply them" />
      <RefHints project={app.project} />
    </Section>
  );
}

export function DomainsTab({ app, canEdit, onEdit }: { app: App; canEdit: boolean; onEdit: () => void }) {
  return (
    <Section id="domains-h" title="Domains" sub={`Public, with TLS, through ${app.node_name}'s Cloudflare Tunnel. No ports open on the node.`}
      actions={canEdit && <button className="btn" onClick={onEdit}><Icon n="globe" />Edit</button>}>
      {app.domains.length === 0 ? <p className="muted">None yet: the app is only reachable inside its project, as <span className="mono">{app.name}:{app.port || 'port'}</span>.</p> : (
        <ul className="domain-list">
          {app.domains.map(d => <li key={d}><a className="mono" href={`https://${d}`} target="_blank" rel="noreferrer">{d}</a></li>)}
        </ul>
      )}
    </Section>
  );
}

export function DeploymentsTab({ app, deps, onOpen }: { app: App; deps: Deployment[]; onOpen: (id: string) => void }) {
  if (deps.length === 0) {
    return <Note>{isDraft(app) ? 'Set a source in General, then Deploy.' : <>Nothing deployed yet. <strong>Deploy</strong> {app.image ? 'pulls the image' : 'builds the branch head'} on {app.node_name}.</>}</Note>;
  }
  return <DeploymentsTable list={deps} app={app} onOpen={d => onOpen(d.id)} />;
}

export function LogsTab({ app }: { app: App }) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState('');
  const box = useRef<HTMLDivElement>(null);
  const load = useCallback(() => {
    if (app.node_down) return;
    api.appLogs(app.name).then(t => { setText(t); setError(''); }, e => setError(e.message));
  }, [app.name, app.node_down]);
  usePoll(load, 5000);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [text]);

  const lines = (text ?? '').split('\n').filter(l => l.trim());
  return (
    <section className="section" aria-labelledby="logs-h">
      <div className="section-head"><h2 id="logs-h">Logs</h2><span className="muted section-note">last 300 lines of the live release · every 5 s</span></div>
      <div className="log" ref={box} role="log" aria-label="Runtime logs">
        {error ? <div className="log-empty">{error}</div>
          : text === null ? <div className="log-empty">Loading…</div>
          : lines.length === 0 ? <div className="log-empty">{app.running ? 'No output yet.' : 'Nothing is running.'}</div>
          : <div className="log-lines">{lines.map((l, i) => <div key={i} className="ln plain"><span className="t">{l}</span></div>)}</div>}
      </div>
    </section>
  );
}

export function AdvancedTab({ app, canEdit, onEditVolumes, onDelete }: { app: App; canEdit: boolean; onEditVolumes: () => void; onDelete: () => void }) {
  return <>
    <Section id="volumes-h" title="Volumes" sub="Data that outlives releases. With any, a deploy stops the running release before starting the new one."
      actions={canEdit && <button className="btn" onClick={onEditVolumes}><Icon n="settings" />Edit</button>}>
      {app.volumes.length === 0 ? <p className="muted">None: every deploy starts from the image's files.</p> : (
        <ul className="domain-list">
          {app.volumes.map(v => <li key={v.target} className="mono od-truncate">{volumeLine(v)}</li>)}
        </ul>
      )}
    </Section>
    <Section id="run-h" title="Runtime" sub={`What runs on ${app.node_name} now.`}>
      <dl className="kv">
        <dt>Container</dt><dd className="mono">{app.running?.container ?? '—'}</dd>
        <dt>Image</dt><dd className="mono">{app.running?.image ?? '—'}</dd>
        <dt>In the project</dt><dd className="mono">{app.name}:{app.port || '—'}</dd>
        <dt>Limits</dt><dd>2 GB of memory, 2048 processes</dd>
      </dl>
    </Section>
    {canEdit && <DangerZone id="danger-h" title="Delete app" sub={`Stops it on ${app.node_name} and removes its images, environment and deployment history.`} onDelete={onDelete} />}
  </>;
}
