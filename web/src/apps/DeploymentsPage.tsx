import { useCallback, useEffect, useState } from 'react';
import { api, type App, type Deployment } from '../api';
import { settling } from '../status';
import { EmptyState, Loading, LoadError, usePoll } from '../ui';
import { DeploymentModal } from './DeploymentModal';
import { DeploymentsTable } from './DeploymentsTable';

// DeploymentsPage is every deploy of every app, with its build log.
export function DeploymentsPage() {
  const [list, setList] = useState<Deployment[] | null>(null);
  const [error, setError] = useState('');
  const [open, setOpen] = useState<Deployment | null>(null);
  const [app, setApp] = useState<App | null>(null);
  const load = useCallback(() => { api.deployments().then(d => { setList(d); setError(''); }, e => setError(e.message)); }, []);
  usePoll(load, list?.some(settling) ? 2000 : 8000);
  useEffect(() => { if (open?.app_name) api.app(open.app_name).then(setApp, () => setApp(null)); else setApp(null); }, [open]);

  return (
    <>
      <h1 className="sr-only">Deployments</h1>
      {error && !list ? (
<LoadError what="deployments" error={error} onRetry={load} />
      ) : !list ? <Loading />
        : list.length === 0 ? (
          <EmptyState icon="deploy" title="No deployments yet" text="Every deploy of every app shows up here, with its build log." action={<a className="btn" href="#/projects">Go to projects</a>} />
        ) : <DeploymentsTable list={list} onOpen={setOpen} />}
      {open && app && <DeploymentModal key={open.id} app={app} id={open.id} onClose={() => setOpen(null)} onRollback={d => { load(); setOpen({ ...d, app_name: app.name }); }} />}
    </>
  );
}
