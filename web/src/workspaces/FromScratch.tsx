import { useEffect, useState } from 'react';
import { api, type GitHub, type ProjectStack } from '../api';
import { Field, FormRow, NodeSelect, Seg, Select, useCan, useToast } from '../ui';
import { Shell, type Common, type Problem } from './NewWorkspace';

// FromScratch starts a project with no repo yet: a devcontainer.json for the stack and a README,
// in a new GitHub repo (with the owner's token) or a local git repo on the node.
export function FromScratch({ c }: { c: Common }) {
  const toast = useToast();
  const { can } = useCan();
  const [error, setError] = useState<Problem>(null);
  const [busy, setBusy] = useState(false);
  const [stacks, setStacks] = useState<ProjectStack[]>([]);
  const [stack, setStack] = useState('');
  const [version, setVersion] = useState('');
  const [gh, setGh] = useState<GitHub | null>(null);
  const [onGitHub, setOnGitHub] = useState(false);
  const [priv, setPriv] = useState(true);
  const ghOK = !!gh?.configured && can('settings'); // repos go on the owner's account

  useEffect(() => {
    api.projectStacks().then(list => {
      setStacks(list);
      if (list[0]) { setStack(list[0].id); setVersion(list[0].versions[0]); }
    }, e => setError({ title: "Couldn't load the stacks", msg: e.message }));
    api.github().then(g => { setGh(g); setOnGitHub(g.configured && can('settings')); }, () => setGh({ configured: false }));
  }, []);
  const chosen = stacks.find(s => s.id === stack);
  const pickStack = (id: string) => { setStack(id); setVersion(stacks.find(s => s.id === id)?.versions[0] ?? ''); };

  const submit = () => {
    setBusy(true);
    api.createFromScratch({ name: c.name, stack, version, github: onGitHub, private: priv, node: c.node })
      .then(w => { toast(onGitHub ? `${w.name}: repo created, workspace on its way` : `${w.name} is being created`); c.onCreated(); },
        e => { setError({ title: "Couldn't create it", msg: e.message }); setBusy(false); });
  };

  return (
    <Shell c={c} error={error} busy={busy} ready={!!stack && !!version} onSubmit={submit}
      sub="A project with no repo yet: it starts with a devcontainer.json for the stack and a README, on a node with the dev role.">
      <Field label="Name" hint={<>DNS label: the repo's name and the SSH user, <span className="mono">{c.name || 'myproject'}.walroos</span>.</>}>
        <input className="input mono" value={c.name} onChange={e => c.setName(e.target.value)} placeholder="myproject" autoFocus />
      </Field>
      <FormRow>
        <Field label="Stack">
          <Select value={stack} onChange={pickStack} disabled={!stacks.length}>
            {stacks.map(s => <option key={s.id} value={s.id}>{s.label}</option>)}
          </Select>
        </Field>
        <Field label={chosen?.version_of ? `${chosen.version_of} version` : 'Version'}>
          <Select mono value={version} onChange={setVersion} disabled={!chosen}>
            {chosen?.versions.map(v => <option key={v} value={v}>{v}</option>)}
          </Select>
        </Field>
      </FormRow>
      <div className="field">
        <span className="label" id="where-h">Repository</span>
        <div className="radio-cards radio-cards-2" role="radiogroup" aria-labelledby="where-h">
          <label className={`radio-card${ghOK ? '' : ' is-off'}`}>
            <span className="rc-h"><input type="radio" name="where" value="github" aria-label="On GitHub" checked={onGitHub} disabled={!ghOK} onChange={() => setOnGitHub(true)} />On GitHub</span>
            <span className="help">{!can('settings')
              ? <>Only the hub's owner can create repos on {gh?.login ? <span className="mono">{gh.login}</span> : 'their account'}.</>
              : gh?.configured
              ? <>Created as <span className="mono">{gh.login}</span>, with the first commit pushed.</>
              : <>Needs a token: add one in <a href="#/settings">Settings</a>.</>}</span>
          </label>
          <label className="radio-card">
            <span className="rc-h"><input type="radio" name="where" value="local" aria-label="Local git repo" checked={!onGitHub} onChange={() => setOnGitHub(false)} />Local git repo</span>
            <span className="help"><span className="mono">git init</span> on the node with the files staged. Commit, add <span className="mono">origin</span> and push when you're ready.</span>
          </label>
        </div>
      </div>
      <FormRow>
        {onGitHub && (
          <Field label="Visibility">
            <Seg label="Visibility" value={priv} onChange={setPriv} options={[[true, 'Private'], [false, 'Public']]} />
          </Field>
        )}
        <NodeSelect role="dev" nodes={c.nodes} value={c.node} onChange={c.setNode} />
      </FormRow>
      {onGitHub && priv && <p className="help">The node clones it with the GitHub token; pushes from the workspace use your own SSH key (forwarded).</p>}
    </Shell>
  );
}
