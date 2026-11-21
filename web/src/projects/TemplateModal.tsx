import { useState } from 'react';
import { api, type App, type Template } from '../api';
import { ErrorAlert, Field, Foot, FormRow, Mark, Modal, NodeSelect, useToast } from '../ui';

// TemplateModal: the catalog of ready-to-run services. Pick one, name it, and it deploys.
export function TemplateModal({ templates, project, onClose, onCreated }: { templates: Template[]; project: string; onClose: () => void; onCreated: (a: App) => void }) {
  const toast = useToast();
  const [q, setQ] = useState('');
  const [cat, setCat] = useState('');
  const [picked, setPicked] = useState<Template | null>(null);
  const [name, setName] = useState('');
  const [node, setNode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const cats = [...new Set(templates.map(t => t.category))];
  const shown = templates.filter(t => (!cat || t.category === cat) &&
    (!q || `${t.name} ${t.description} ${t.category}`.toLowerCase().includes(q.toLowerCase())));
  const pick = (t: Template) => { setPicked(t); setName(t.id); };
  const submit = () => {
    if (!picked) return;
    setBusy(true);
    api.createFromTemplate({ template: picked.id, name, project, node }).then(a => { toast(`${a.name} is deploying`); onCreated(a); },
      e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title="Templates" wide onClose={onClose} sub="Common services, ready to run. They get their volumes and generated passwords, and deploy right away."
      foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!picked || !name} icon="deploy" label={picked ? `Deploy ${picked.name}` : 'Deploy'} busyLabel="Creating…" />}>
      {error && <ErrorAlert title="Couldn't create it" msg={error} />}
      <div className="form">
        <div className="toolbar">
          <input className="input" type="search" value={q} onChange={e => setQ(e.target.value)} placeholder="Search templates…" aria-label="Search templates" autoFocus />
        </div>
        <div className="chips" role="group" aria-label="Category">
          <button aria-pressed={!cat} onClick={() => setCat('')}>All</button>
          {cats.map(c => <button key={c} aria-pressed={cat === c} onClick={() => setCat(cat === c ? '' : c)}>{c}</button>)}
        </div>
        <div className="picker">
          <div className="tile-pick templates" role="radiogroup" aria-label="Template">
            {shown.map(t => (
              <label key={t.id}>
                <input type="radio" name="template" value={t.id} aria-label={t.name} checked={picked?.id === t.id} onChange={() => pick(t)} />
                <Mark logo={t.logo} size={24} />
                <span><strong>{t.name}</strong><span className="help tile-sub">{t.category}</span></span>
                <span className="help">{t.description}</span>
              </label>
            ))}
          </div>
          {shown.length === 0 && <p className="muted">No template matches.</p>}
        </div>
        {picked && (
          <FormRow>
            <Field label="Name" hint={<>The project reaches it as <span className="mono">{name || 'name'}:{picked.port}</span>.</>}>
              <input className="input mono" value={name} onChange={e => setName(e.target.value)} />
            </Field>
            <NodeSelect role="prod" value={node} onChange={setNode} />
          </FormRow>
        )}
      </div>
    </Modal>
  );
}
