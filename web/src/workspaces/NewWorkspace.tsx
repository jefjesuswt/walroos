import { useState, type ReactNode } from 'react';
import { api, type Detection, type Node } from '../api';
import { ErrorAlert, Field, Foot, FormRow, Icon, Modal, NodeSelect, Seg, useToast } from '../ui';
import { DetectedPanel, devcontainerText, suggestName } from './Detected';
import { FromScratch } from './FromScratch';

export type Problem = { title: string; msg: string } | null;
// What both ways of creating a workspace share: the name, the node, the Source switch.
export type Common = {
  name: string; setName: (v: string) => void; node: string; setNode: (v: string) => void; nodes: Node[];
  seg: ReactNode; onClose: () => void; onCreated: () => void;
};

// NewWorkspaceModal creates a workspace from a Git repo, or a new project from scratch. Each way
// is its own form; the name and node carry over when you switch.
export function NewWorkspaceModal({ nodes, onClose, onCreated }: { nodes: Node[]; onClose: () => void; onCreated: () => void }) {
  const [fresh, setFresh] = useState(false); // New project: no repo yet
  const [name, setName] = useState('');
  const [node, setNode] = useState('');
  const seg = <Seg label="Source" value={fresh} onChange={setFresh} options={[[false, 'From repo'], [true, 'From scratch']]} />;
  const c: Common = { name, setName, node, setNode, nodes, seg, onClose, onCreated };
  return fresh ? <FromScratch c={c} /> : <FromRepo c={c} />;
}

// Shell is the dialog around either form.
export function Shell({ c, sub, error, busy, ready, onSubmit, children }: {
  c: Common; sub: string; error: Problem; busy: boolean; ready: boolean; onSubmit: () => void; children: ReactNode;
}) {
  return (
    <Modal title="New workspace" wide onClose={c.onClose} sub={sub}
      foot={<Foot onCancel={c.onClose} onClick={onSubmit} busy={busy} disabled={!c.name || !ready} label="Create workspace" busyLabel="Creating…" />}>
      {error && <ErrorAlert title={error.title} msg={error.msg} />}
      <div className="form">{c.seg}{children}</div>
    </Modal>
  );
}

function FromRepo({ c }: { c: Common }) {
  const toast = useToast();
  const [error, setError] = useState<Problem>(null);
  const [busy, setBusy] = useState(false);
  const [repo, setRepo] = useState('');
  const [branch, setBranch] = useState('');
  const [dcPath, setDcPath] = useState('');
  const [detecting, setDetecting] = useState(false);
  const [found, setFound] = useState<Detection | null>(null);
  const [off, setOff] = useState<string[]>([]); // postCreate tools turned off
  const [dcText, setDcText] = useState('');     // the devcontainer.json that will be sent, editable

  const generated = !!found?.devcontainer;
  let dcError = '';
  let dc: Record<string, unknown> | undefined;
  if (generated) {
    try { dc = JSON.parse(dcText); } catch (e) { dcError = (e as Error).message; }
  }

  const detect = () => {
    setDetecting(true); setError(null);
    api.detect(repo, branch, c.node).then(d => {
      setFound(d); setOff([]); setDcText(devcontainerText(d, []));
      if (!c.name) c.setName(suggestName(repo));
      if (!branch) setBranch(d.branch);
    }, e => setError({ title: "Couldn't detect", msg: e.message })).finally(() => setDetecting(false));
  };
  const toggle = (id: string) => {
    const next = off.includes(id) ? off.filter(x => x !== id) : [...off, id];
    setOff(next);
    if (found) setDcText(devcontainerText(found, next));
  };
  const submit = () => { setBusy(true); api.createWorkspace({ name: c.name, repo_url: repo, branch, devcontainer_path: generated ? '' : dcPath, node: c.node, devcontainer: dc, stack: found?.stack })
    .then(w => { toast(`${w.name} is being created`); c.onCreated(); }, e => { setError({ title: "Couldn't create it", msg: e.message }); setBusy(false); }); };

  return (
    <Shell c={c} error={error} busy={busy} ready={!!repo && !dcError} onSubmit={submit}
      sub="Cloned from a Git repo and run on a node with the dev role. A devcontainer.json is used when present, else the walroos base image.">
    <Field label="Repository" hint="The node clones with its own git: public repos, or ones its SSH agent can read. Detect proposes a devcontainer for the stack.">
      <div className="input-btn">
        <input className="input mono" value={repo} onChange={e => { setRepo(e.target.value); setFound(null); }} placeholder="git@github.com:me/app.git" autoFocus />
        <button type="button" className="btn btn-tonal" disabled={!repo || detecting} onClick={detect}>
          {detecting ? <><Icon n="refresh" cls="spin" />Detecting…</> : 'Detect'}
        </button>
      </div>
    </Field>
    {found && <DetectedPanel d={found} off={off} toggle={toggle} dcText={dcText} setDcText={setDcText} dcError={dcError} onDrop={() => setFound(null)} />}
    <Field label="Name" hint={<>DNS label. It's also the SSH user: <span className="mono">{c.name || 'myproject'}.walroos</span>.</>}>
      <input className="input mono" value={c.name} onChange={e => c.setName(e.target.value)} placeholder="myproject" />
    </Field>
    <FormRow>
      <Field label="Branch" optional>
        <input className="input mono" value={branch} onChange={e => setBranch(e.target.value)} placeholder="default branch" />
      </Field>
      <NodeSelect role="dev" nodes={c.nodes} value={c.node} onChange={c.setNode} />
    </FormRow>
    {!generated && (
      <Field label="devcontainer.json" optional hint="Leave empty to use the standard location, or the base image if the repo has none.">
        <input className="input mono" value={dcPath} onChange={e => setDcPath(e.target.value)} placeholder=".devcontainer/devcontainer.json" />
      </Field>
    )}
    </Shell>
  );
}
