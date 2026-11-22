// How an app runs and proves it's up: its health check (HTTP, TCP or just running), the command
// replacing the image's, and the repo folder it builds from (a monorepo's service).
import type { App, Health } from '../api';
import { Disclosure, Field, FormRow, Seg } from '../ui';
import { joinCommand } from './command';

export type Run = { health: Health; port: string; health_path: string; command: string; context: string };

export const runOf = (a: App): Run => ({ health: a.health || 'http', port: a.port ? String(a.port) : '', health_path: a.health_path,
  command: joinCommand(a.command ?? []), context: a.context ?? '' });

const CHECKS: [Health, string][] = [['http', 'HTTP'], ['tcp', 'TCP'], ['running', 'Running']];
const HINT: Record<string, string> = {
  http: 'A GET on the path must answer 2xx or 3xx within 90 s. For web apps and APIs.',
  tcp: 'The port must accept connections within 90 s. For databases, caches and anything not HTTP.',
  running: 'It must keep running for 5 s without restarting. For workers with no port.',
};

// RunFields edits it; fromRepo shows the build folder, staticSite hides what a static site fixes.
export function RunFields({ value, onChange, fromRepo, staticSite }: {
  value: Run; onChange: (r: Run) => void; fromRepo: boolean; staticSite: boolean;
}) {
  const set = (k: keyof Run) => (e: { target: { value: string } }) => onChange({ ...value, [k]: e.target.value });
  return <>
    {!staticSite && (
      <Field label="Health check" group hint={HINT[value.health || 'http']}>
        <Seg label="Health check" value={value.health || 'http'} onChange={health => onChange({ ...value, health })} options={CHECKS} />
      </Field>
    )}
    <FormRow>
      {!staticSite && <Field label="Port" optional={value.health === 'running'} hint="What the container listens on.">
        <input className="input mono" value={value.port} onChange={set('port')} placeholder="3000" inputMode="numeric" />
      </Field>}
      {(staticSite || value.health === 'http' || !value.health) && <Field label="Health check path" optional>
        <input className="input mono" value={value.health_path} onChange={set('health_path')} placeholder="/" />
      </Field>}
    </FormRow>
    <Disclosure summary={staticSite ? 'Build folder' : fromRepo ? 'Command and build folder' : 'Command'}>
      {!staticSite && <Field label="Command" optional hint={'Replaces the image\'s command. Quote arguments with spaces: sh -c "npm run worker".'}>
        <input className="input mono" value={value.command} onChange={set('command')} placeholder="the image's own" />
      </Field>}
      {fromRepo && <Field label="Build folder" optional hint="A folder of the repo to build from, like a monorepo's service. The Dockerfile is relative to it.">
        <input className="input mono" value={value.context} onChange={set('context')} placeholder="the repo's root" />
      </Field>}
    </Disclosure>
  </>;
}

// RunSummary is its rows in the source's summary.
export function RunSummary({ app }: { app: App }) {
  const check = app.health === 'tcp' ? `TCP on port ${app.port} within 90 s`
    : app.health === 'running' ? 'keeps running for 5 s'
    : `GET ${app.health_path || '/'} → 2xx/3xx within 90 s`;
  return <>
    <dt>Port</dt><dd className="mono">{app.port || 'none (a worker)'}</dd>
    <dt>Health check</dt><dd className="mono">{check}</dd>
    {!!app.command?.length && <><dt>Command</dt><dd className="mono">{joinCommand(app.command)}</dd></>}
    {app.context && <><dt>Build folder</dt><dd className="mono">{app.context}</dd></>}
  </>;
}
