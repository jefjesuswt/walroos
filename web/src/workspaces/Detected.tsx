// What Detect found in a repo: the stack, its tools and the devcontainer.json walroos proposes.
import type { Detection } from '../api';
import { Check, Disclosure } from '../ui';

// The generated config with the turned-off tools' installs left out of postCreateCommand.
export function devcontainerText(d: Detection, off: string[]): string {
  const { postCreateCommand: _, ...dc } = d.devcontainer ?? {};
  const cmds = d.tools.filter(t => t.install && !off.includes(t.id)).map(t => t.install);
  return JSON.stringify(cmds.length ? { ...dc, postCreateCommand: cmds.join(' && ') } : dc, null, 2);
}

// github.com/me/My_App.git → my-app
export const suggestName = (repo: string) =>
  (repo.replace(/\/+$/, '').split(/[/:]/).pop() ?? '').replace(/\.git$/, '').toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63);

export function DetectedPanel({ d, off, toggle, dcText, setDcText, dcError, onDrop }: {
  d: Detection; off: string[]; toggle: (id: string) => void; dcText: string; setDcText: (s: string) => void; dcError: string; onDrop: () => void;
}) {
  const title = d.stack === 'devcontainer' ? "Uses the repo's devcontainer" : d.stack === 'none' ? 'No stack detected' : `Detected ${d.stack}`;
  return (
    <section className="detected" aria-label="Detected stack">
      <div className="detected-head">
        <div className="detected-t">
          <strong>{title}</strong>
          {d.image && <span className="mono muted od-truncate" title={d.image}>{d.image}{d.remote_user ? ` · ${d.remote_user}` : ''}</span>}
        </div>
        {d.devcontainer && <button type="button" className="btn btn-ghost" onClick={onDrop}>Don't use</button>}
      </div>
      {d.devcontainer && d.tools.length > 0 && (
        <ul className="tools">
          {d.tools.map(t => (
            <li key={t.id}>
              <Check checked={t.source === 'image' || !off.includes(t.id)} disabled={t.source === 'image'} onChange={() => toggle(t.id)}>
                <span>{t.label}{t.version && <span className="mono muted"> {t.version}</span>}</span>
              </Check>
              <span className="chip">{t.source === 'image' ? 'in image' : 'postCreate'}</span>
            </li>
          ))}
        </ul>
      )}
      {d.notes?.map(n => <p key={n} className="help">{n}</p>)}
      {d.devcontainer && (
        <Disclosure summary="devcontainer.json">
          <div>
            <textarea className="mono" rows={10} value={dcText} onChange={e => setDcText(e.target.value)} aria-invalid={!!dcError} aria-label="Generated devcontainer.json" spellCheck={false} />
            <span className={dcError ? 'err' : 'help'}>{dcError || "Sent with the workspace, never written to the repo. Toggling a tool regenerates it."}</span>
          </div>
        </Disclosure>
      )}
    </section>
  );
}
