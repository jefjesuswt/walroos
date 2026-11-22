import { useCallback, useState } from 'react';
import { api, type App, type Deployment } from '../api';
import { appState, isDraft, settling, short } from '../status';
import { Alert, Badge, BrandLogo, Icon, Loading, LoadFailed, PageHeader, Tabs, useCan, usePoll, useToast, useTrail } from '../ui';
import { AutomationTab } from './AutomationTab';
import { DeploymentModal } from './DeploymentModal';
import { DeleteApp, DeployModal, DomainsModal, VolumesModal } from './dialogs';
import { SourceCard } from './SourceCard';
import { AdvancedTab, DeploymentsTab, DomainsTab, EnvironmentTab, LogsTab } from './tabs';

type AppTab = 'general' | 'environment' | 'domains' | 'automation' | 'deployments' | 'logs' | 'advanced';
const TABS: [AppTab, string][] = [['general', 'General'], ['environment', 'Environment'], ['domains', 'Domains'],
  ['automation', 'Automation'], ['deployments', 'Deployments'], ['logs', 'Logs'], ['advanced', 'Advanced']];

type Dialog = null | 'deploy' | 'domains' | 'volumes' | 'delete';

// AppPage is one app: its header and actions, and a tab per part of its setup.
export function AppPage({ name }: { name: string }) {
  const { can } = useCan();
  const toast = useToast();
  const [a, setA] = useState<App | null>(null);
  const [deps, setDeps] = useState<Deployment[]>([]);
  const [error, setError] = useState('');
  const [tab, setTab] = useState<AppTab>('general');
  const [dialog, setDialog] = useState<Dialog>(null);
  const [open, setOpen] = useState<string | null>(null); // deployment id in the log modal

  const load = useCallback(() => {
    api.app(name).then(x => { setA(x); setError(''); }, e => setError(e.message));
    api.deployments(name).then(setDeps, () => undefined);
  }, [name]);
  usePoll(load, settling(deps[0]) ? 2000 : 8000);
  useTrail(a ? [{ label: 'Projects', href: '#/projects' }, { label: a.project, href: `#/projects/${a.project}` }] : null);

  if (error && !a) return <LoadFailed what="app" error={error} notFound={error === 'app not found'} back="#/projects" backLabel="Back to projects" />;
  if (!a) return <Loading />;

  const [s, label] = appState(a);
  const canEdit = can('apps');
  const close = () => setDialog(null);
  const saved = () => { close(); load(); };

  return (
    <>
      <PageHeader mono title={a.name} badge={<Badge s={s} label={label} />}
        mark={isDraft(a) ? <Icon n="apps" /> : <BrandLogo name={a.image ? 'docker' : 'git'} />}
        meta={<>
          {a.description && <span>{a.description}</span>}
          <span><Icon n="nodes" /><span className="mono">{a.node_name}</span></span>
          {a.running && <span>live <span className="sha">{short(a.running.sha)}</span></span>}
        </>}
        actions={canEdit && (
          <button className="btn btn-primary" disabled={a.node_down || isDraft(a)} title={isDraft(a) ? 'Set its source first' : undefined} onClick={() => setDialog('deploy')}><Icon n="deploy" />Deploy</button>
        )} />

      {a.node_down && <Alert title={`${a.node_name} isn't answering`}>Deploys and logs wait for it. The app keeps running if the node is up but the hub can't reach it.</Alert>}

      <Tabs tabs={TABS} value={tab} onChange={setTab} />

      {tab === 'general' && <SourceCard app={a} canEdit={canEdit} onSaved={x => { setA(x); toast(isDraft(a) ? 'Source set: deploy it when ready' : 'Source saved: it applies on the next deploy'); }} />}
      {tab === 'environment' && <EnvironmentTab app={a} canEdit={canEdit} />}
      {tab === 'domains' && <DomainsTab app={a} canEdit={canEdit} onEdit={() => setDialog('domains')} />}
      {tab === 'automation' && <AutomationTab app={a} canEdit={canEdit} onChange={setA} />}
      {tab === 'deployments' && <DeploymentsTab app={a} deps={deps} onOpen={setOpen} />}
      {tab === 'logs' && <LogsTab app={a} />}
      {tab === 'advanced' && <AdvancedTab app={a} canEdit={canEdit} onEditVolumes={() => setDialog('volumes')} onDelete={() => setDialog('delete')} />}

      {dialog === 'deploy' && <DeployModal app={a} onClose={close} onQueued={d => { close(); load(); setTab('deployments'); setOpen(d.id); }} />}
      {dialog === 'volumes' && <VolumesModal app={a} onClose={close} onSaved={saved} />}
      {dialog === 'domains' && <DomainsModal app={a} onClose={close} onSaved={saved} />}
      {dialog === 'delete' && <DeleteApp app={a} onClose={close} />}
      {open && <DeploymentModal key={open} app={a} id={open} onClose={() => setOpen(null)} onRollback={d => { load(); setOpen(d.id); }} />}
    </>
  );
}
