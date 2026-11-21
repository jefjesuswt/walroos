// RefHints shows, under an app's variables, what its values can reference in its project:
// ${{shop-db.URL}}, ${{api.URL}}, ${{shared.STRIPE_KEY}}. Clicking one copies it.
import { useEffect, useState } from 'react';
import { api, type Ref } from '../api';
import { copyText, useToast } from '../ui';

export function RefHints({ project }: { project: string }) {
  const toast = useToast();
  const [refs, setRefs] = useState<Ref[] | null>(null);
  useEffect(() => { api.projectRefs(project).then(setRefs, () => setRefs([])); }, [project]);
  if (!refs) return null;
  const copy = (ref: string) => copyText(ref).then(() => toast(`Copied ${ref}`), () => toast('Copy failed: select it and copy it', 'bad'));
  return (
    <div className="refs">
      <p className="help">
        Reference another service of <a href={`#/projects/${project}`}>{project}</a> or a shared variable: the value is filled in on each deploy, so it follows changes.
      </p>
      {refs.map(r => (
        <div key={r.service} className="refs-row">
          <span className="refs-svc mono">{r.service}</span>
          <span className="refs-keys">
            {r.keys.length === 0
              ? <span className="muted">{r.service === 'shared' ? 'none yet: add them with Variables on the project' : 'nothing to reference'}</span>
              : r.keys.map(k => {
                const ref = `\${{${r.service}.${k}}}`;
                return <button key={k} type="button" className="chip refs-chip mono" title={`Copy ${ref}`} onClick={() => copy(ref)}>{k}</button>;
              })}
          </span>
        </div>
      ))}
    </div>
  );
}
