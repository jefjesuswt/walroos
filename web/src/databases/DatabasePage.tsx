import { useCallback, useState } from 'react';
import { api, type Backup, type Database } from '../api';
import { dbLabel, dbState, engineLogo } from '../status';
import { ago, Alert, Badge, Confirm, Icon, Loading, LoadFailed, Mark, PageHeader, Tabs, usePoll, useToast, useTrail } from '../ui';
import { DeleteDatabase, LinkModal, ScheduleModal } from './dialogs';
import { AdvancedTab, BackupsTab, ConnectionCard } from './tabs';

type Tab = 'general' | 'backups' | 'advanced';
const TABS: [Tab, string][] = [['general', 'General'], ['backups', 'Backups'], ['advanced', 'Advanced']];

// DatabasePage is one database: how apps reach it, its backups, and deleting it.
export function DatabasePage({ name }: { name: string }) {
  const toast = useToast();
  const [d, setD] = useState<Database | null>(null);
  const [backups, setBackups] = useState<Backup[] | null>(null);
  const [error, setError] = useState('');
  const [dialog, setDialog] = useState<null | 'link' | 'schedule' | 'delete'>(null);
  const [restoring, setRestoring] = useState<Backup | null>(null);
  const [backingUp, setBackingUp] = useState(false);
  const [tab, setTab] = useState<Tab>('general');

  const load = useCallback(() => {
    api.database(name).then(x => { setD(x); setError(''); }, e => setError(e.message));
    api.backups(name).then(setBackups, () => setBackups([]));
  }, [name]);
  usePoll(load, d?.state === 'activating' ? 2000 : 15000);
  useTrail(d ? [{ label: 'Projects', href: '#/projects' }, { label: d.project, href: `#/projects/${d.project}` }] : null);

  if (error && !d) return <LoadFailed what="database" error={error} notFound={error === 'database not found'} back="#/projects" backLabel="Back to projects" />;
  if (!d) return <Loading />;

  const [s, l] = dbState(d);
  const close = () => setDialog(null);
  const backupNow = () => {
    setBackingUp(true);
    api.backupNow(d.name).then(b => { toast(`Backed up: ${b.file}`); load(); }, e => { toast(e.message, 'bad'); load(); })
      .finally(() => setBackingUp(false));
  };
  const restore = (b: Backup) => {
    setRestoring(null);
    toast(`Restoring ${b.file}…`);
    api.restore(d.name, b.file).then(() => { toast(`${d.name} restored from ${b.file}`); load(); }, e => toast(e.message, 'bad'));
  };

  return (
    <>
      <PageHeader mono title={d.name} badge={<Badge s={s} label={l} />} mark={<Mark logo={engineLogo(d.engine)} icon="database" />}
        meta={<>
          {d.description && <span>{d.description}</span>}
          <span>{dbLabel(d)}</span>
          <span><Icon n="nodes" /><span className="mono">{d.node_name}</span></span>
        </>}
        actions={<>
          <button className="btn btn-primary" disabled={d.node_down || backingUp} onClick={backupNow}><Icon n="upload" />{backingUp ? 'Backing up…' : 'Back up now'}</button>
          <button className="btn" onClick={() => setDialog('link')}><Icon n="plug" />Link to app</button>
        </>} />

      {d.node_down && <Alert title={`${d.node_name} isn't answering`}>Backups wait for it. The database keeps running if the node is up but the hub can't reach it.</Alert>}
      {d.state === 'missing' && <Alert title={`${d.node_name} doesn't have it`}>Its unit was removed on the node. Delete it here and create it again; the data volume is still there unless it was purged.</Alert>}

      <Tabs tabs={TABS} value={tab} onChange={setTab} />
      {tab === 'general' && <ConnectionCard d={d} />}
      {tab === 'backups' && <BackupsTab d={d} backups={backups} onSchedule={() => setDialog('schedule')} onRestore={setRestoring} />}
      {tab === 'advanced' && <AdvancedTab d={d} onDelete={() => setDialog('delete')} />}

      {dialog === 'link' && <LinkModal d={d} onClose={close} />}
      {dialog === 'schedule' && <ScheduleModal d={d} onClose={close} onSaved={x => { close(); setD(x); }} />}
      {dialog === 'delete' && <DeleteDatabase d={d} onClose={close} />}
      {restoring && (
        <Confirm title={`Restore ${d.name}?`} confirmLabel="Restore" danger onClose={() => setRestoring(null)} onConfirm={() => restore(restoring)}>
          <p className="muted">Replaces what's in <span className="mono">{d.name}</span> with the dump from {ago(restoring.created_at)} (<span className="mono">{restoring.file}</span>). Anything written since is lost. Back up now first if you may want it back.</p>
        </Confirm>
      )}
    </>
  );
}
