import { useCallback, useEffect, useRef, useState } from 'react';
import { api, type App, type Deployment } from '../api';
import { duration, isCurrent, settling, short } from '../status';
import { ago, Alert, Icon, Modal, useCan, usePoll, useToast } from '../ui';

// DeploymentModal shows one deployment's build log, live while it runs.
export function DeploymentModal({ app, id, onClose, onRollback }: {
  app: App; id: string; onClose: () => void; onRollback: (d: Deployment) => void;
}) {
  const toast = useToast();
  const [d, setD] = useState<Deployment | null>(null);
  const [error, setError] = useState('');
  const box = useRef<HTMLDivElement>(null);
  const load = useCallback(() => { api.deployment(app.name, id).then(setD, e => setError(e.message)); }, [app.name, id]);
  usePoll(load, settling(d ?? undefined) || !d ? 1500 : 60000);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [d?.log]);

  const { can } = useCan();
  const canRollback = can('apps') && d?.state === 'succeeded' && !!d.image && !isCurrent(app, d) && !app.node_down;
  const rollback = () => d && api.rollback(app.name, d.id).then(
    next => { toast(`Rolling back ${app.name} to ${short(d.sha)}`); onRollback(next); },
    e => toast(e.message, 'bad'),
  );
  const lines = (d?.log ?? '').split('\n').filter(l => l !== '');

  return (
    <Modal wide onClose={onClose} title={`${app.name} · ${short(d?.sha ?? '') || 'deploy'}`}
      sub={d ? `${d.state}${isCurrent(app, d) ? ' · current release' : ''} · started ${ago(d.created_at)}${settling(d) ? '' : ` · took ${duration(d)}`}` : 'Loading…'}
      foot={canRollback ? <><button className="btn" onClick={onClose}>Close</button><button className="btn btn-primary" onClick={rollback}><Icon n="rollback" />Roll back to this</button></> : undefined}>
      {error && <Alert title="Couldn't load it">{error}</Alert>}
      {d?.state === 'failed' && d.error && (
        <Alert title={<span className="mono break-any">{d.error}</span>}>
          {app.running ? <>The previous release keeps serving: <span className="sha">{short(app.running.sha)}</span>.</> : 'Nothing was released.'}
        </Alert>
      )}
      <div className="log log-short" ref={box} role="log" aria-label="Deployment log">
        {lines.length === 0 ? <div className="log-empty">{d?.state === 'queued' ? 'Waiting for the deploy before it to finish…' : 'No output yet.'}</div>
          : <div className="log-lines">{lines.map((l, i) => (
            <div key={i} className={`ln plain${/never got healthy|^error|failed|fatal/i.test(l) ? ' err' : /^(cloning|building|starting|health check|stopping|rollback)|is live/.test(l) ? '' : ' dim'}`}><span className="t">{l}</span></div>
          ))}</div>}
      </div>
    </Modal>
  );
}
