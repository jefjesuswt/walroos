// A static site's build settings, like Netlify's: runtime, install and build commands, the folder
// to serve and SPA routing. Every field is optional; walroos finds them from the repo.
import type { StaticBuild } from '../api';
import { Check, Field, FormRow, Seg } from '../ui';

const RUNTIMES: [NonNullable<StaticBuild['runtime']>, string][] = [['', 'Auto'], ['node', 'Node'], ['bun', 'Bun']];

export function StaticFields({ value, onChange }: { value: StaticBuild; onChange: (v: StaticBuild) => void }) {
  const set = (k: 'install' | 'build' | 'publish') => (e: { target: { value: string } }) => onChange({ ...value, [k]: e.target.value });
  return <>
    <Field label="Runtime" group hint="Auto uses Bun with a bun lockfile or a BUN_VERSION variable, else Node (npm, pnpm or yarn by the lockfile). NODE_VERSION, BUN_VERSION or the repo's .nvmrc pick the version; default Node 24, Bun 1.">
      <Seg label="Runtime" value={value.runtime ?? ''} onChange={runtime => onChange({ ...value, runtime })} options={RUNTIMES} />
    </Field>
    <FormRow>
      <Field label="Install command" optional>
        <input className="input mono" value={value.install ?? ''} onChange={set('install')} placeholder="npm ci" />
      </Field>
      <Field label="Build command" optional hint="Default: the build script in package.json.">
        <input className="input mono" value={value.build ?? ''} onChange={set('build')} placeholder="npm run build" />
      </Field>
    </FormRow>
    <Field label="Publish directory" optional hint="Default: the first of dist, build, out or public with an index.html (Angular: dist/<app>/browser).">
      <input className="input mono" value={value.publish ?? ''} onChange={set('publish')} placeholder="dist" />
    </Field>
    <Check checked={!!value.spa} onChange={spa => onChange({ ...value, spa })}>
      Single-page app: paths with no file get index.html (React Router, Vue Router, Angular)
    </Check>
    <p className="help">The app's environment variables are there during the build (VITE_*, NEXT_PUBLIC_*…): a change applies on the next deploy.</p>
  </>;
}

// StaticSummary is the static site's rows in the source's summary.
export function StaticSummary({ value }: { value: StaticBuild }) {
  return <>
    <dt>Runtime</dt><dd>{RUNTIMES.find(([r]) => r === value.runtime)?.[1] ?? 'Auto'}{!value.runtime && ' (from the lockfile)'}</dd>
    <dt>Install</dt><dd className="mono">{value.install || 'auto'}</dd>
    <dt>Build command</dt><dd className="mono">{value.build || 'the build script, if any'}</dd>
    <dt>Publish</dt><dd className="mono">{value.publish || 'found after the build'}</dd>
    <dt>Routing</dt><dd>{value.spa ? 'Single-page app (unknown paths → index.html)' : 'Files only (unknown paths → 404.html)'}</dd>
  </>;
}
