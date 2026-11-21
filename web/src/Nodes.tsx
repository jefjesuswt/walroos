import { useCallback, useState } from 'react';
import { api, ROLES, type JoinToken, type Node, type Role } from './api';
import { ago, Badge, Chip, Cmd, Confirm, EmptyState, Icon, Meter, Modal, usePoll, useToast } from './ui';

export function NodesPage({ addOpen, setAddOpen }: { addOpen: boolean; setAddOpen: (v: boolean) => void }) {
  const toast = useToast();
  const [nodes, setNodes] = useState<Node[] | null>(null);
  const [error, setError] = useState('');
  const [removing, setRemoving] = useState<Node | null>(null);

  const load = useCallback(() => {
    api.nodes().then(n => { setNodes(n); setError(''); }, e => setError(e.message));
  }, []);
  usePoll(load, 5000); // heartbeats arrive every 15s

  const remove = (n: Node) =>
    api.removeNode(n.id).then(
      () => { toast(`${n.name} removed`); setRemoving(null); load(); },
      e => toast(e.message, 'bad'),
    );

  const online = nodes?.filter(n => n.status === 'online').length ?? 0;

  return (
    <>
      <h1 className="sr-only">Nodes</h1>
      {nodes && nodes.length > 0 && (
        <div className="page-head page-bar"><p className="muted page-sub">{online} of {nodes.length} online</p><span /></div>
      )}

      {error && !nodes ? (
        <div className="state state-err" role="alert">
          <Icon n="alert" cls="ic-lg" /><h3>Couldn't load nodes</h3>
          <p className="muted">{error}</p>
          <button className="btn" onClick={load}><Icon n="refresh" />Retry</button>
        </div>
      ) : !nodes ? (
        <div className="skel-list" aria-busy="true" aria-label="Loading">
          {[0, 1, 2].map(i => <div key={i} className="skel-row"><i className="skel w30" /><i className="skel w50" /><i className="skel w15" /></div>)}
        </div>
      ) : nodes.length === 0 ? (
        <EmptyState icon="nodes" title="No nodes yet"
          text="Join a machine to the hub over Tailscale. One command, then it shows up here."
          action={<button className="btn btn-primary" onClick={() => setAddOpen(true)}><Icon n="plus" />Add node</button>} />
      ) : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr>
              <th className="col-grow">Node</th><th className="hide-sm">Roles</th><th>Status</th>
              <th className="hide-sm" style={{ minWidth: 100 }}>CPU</th><th className="hide-sm" style={{ minWidth: 130 }}>RAM</th>
              <th className="hide-md" style={{ minWidth: 130 }}>Disk</th><th className="hide-md">Podman</th>
              <th className="hide-sm">Last seen</th><th><span className="sr-only">Actions</span></th>
            </tr></thead>
            <tbody>{nodes.map(n => <NodeRow key={n.id} n={n} onRemove={() => setRemoving(n)} />)}</tbody>
          </table>
        </div>
      )}

      {addOpen && <AddNodeModal known={nodes ?? []} onClose={() => setAddOpen(false)} onJoined={load} />}

      {removing && (
        <Confirm title={`Remove ${removing.name}?`} confirmLabel="Remove node" danger onClose={() => setRemoving(null)} onConfirm={() => remove(removing)}>
          <p className="muted">Its token stops working and the agent exits on the next heartbeat. Containers on the machine keep running. To bring it back, join it again with a new token.</p>
        </Confirm>
      )}
    </>
  );
}

function NodeRow({ n, onRemove }: { n: Node; onRemove: () => void }) {
  const t = n.telemetry;
  const live = n.status === 'online';
  const dash = <span className="muted">—</span>;
  const gb = (mb: number) => (mb / 1024).toFixed(1);
  return (
    <tr>
      <td className="col-grow"><div className="cell-stack">
        <span className="cell-title mono" title={n.name}>{n.name}</span>
        <span className="cell-sub mono od-truncate">{[n.address, t.os, t.walroos && `walroos ${t.walroos}`].filter(Boolean).join(' · ') || 'waiting for first heartbeat'}</span>
      </div></td>
      <td className="hide-sm roles-cell"><div className="od-cluster" style={{ ['--od-gap' as string]: '4px' }}>{n.roles.map(r => <Chip key={r}>{r}</Chip>)}</div></td>
      <td><Badge s={n.status} /></td>
      <td className="hide-sm">{live ? <Meter label="CPU" pct={t.cpu_percent} text={`${Math.round(t.cpu_percent)}%`} /> : dash}</td>
      <td className="hide-sm">{live && t.mem_total_mb ? <Meter label="RAM" pct={t.mem_used_mb / t.mem_total_mb * 100} text={`${gb(t.mem_used_mb)}/${gb(t.mem_total_mb)} GB`} /> : dash}</td>
      <td className="hide-md">{live && t.disk_total_gb ? <Meter label="Disk" pct={t.disk_used_gb / t.disk_total_gb * 100} text={`${t.disk_used_gb}/${t.disk_total_gb} GB`} /> : dash}</td>
      <td className="hide-md">{t.podman ? <div className="cell-stack"><span className="mono">{t.podman}</span>
        <span className="cell-sub od-nowrap">{t.containers} containers</span></div> : dash}</td>
      <td className="hide-sm od-nowrap" style={n.status === 'offline' ? { color: 'var(--bad)' } : undefined}>
        <span className={n.status === 'offline' ? '' : 'muted'}>{ago(n.last_seen)}</span>
      </td>
      <td className="ta-r"><button className="btn-icon" aria-label={`Remove ${n.name}`} title="Remove node" onClick={onRemove}><Icon n="trash" /></button></td>
    </tr>
  );
}

function AddNodeModal({ known, onClose, onJoined }: { known: Node[]; onClose: () => void; onJoined: () => void }) {
  const [jt, setJt] = useState<JoinToken | null>(null);
  const [error, setError] = useState('');
  const [roles, setRoles] = useState<Role[]>(['dev']);
  const [joined, setJoined] = useState<Node | null>(null);
  const [knownIds] = useState(() => new Set(known.map(n => n.id)));
  const toast = useToast();

  usePoll(() => {
    if (!jt) {
      api.joinToken().then(setJt, e => setError(e.message));
      return;
    }
    if (joined) return;
    api.nodes().then(list => {
      const fresh = list.find(n => !knownIds.has(n.id));
      if (fresh) { setJoined(fresh); onJoined(); toast(`${fresh.name} joined the hub`); }
    });
  }, 3000);

  const toggle = (r: Role) => setRoles(rs => rs.includes(r) ? rs.filter(x => x !== r) : ROLES.filter(x => x === r || rs.includes(x)));
  const command = jt && `${jt.command}${roles.join(',') === 'dev' ? '' : ` --roles ${roles.join(',')}`}`;

  return (
    <Modal title="Add node" wide onClose={onClose}
      sub="Run this on the machine you want to join. It needs Tailscale up; Podman for workspaces and apps.">
      {error ? (
        <div className="alert alert-bad" role="alert"><Icon n="alert" /><div className="alert-text"><strong>Couldn't create a join token</strong><span className="muted">{error}</span></div></div>
      ) : (
        <>
          <div className="od-cluster" role="group" aria-label="Roles" style={{ ['--od-gap' as string]: '16px' }}>
            <span className="label">Roles</span>
            {ROLES.map(r => (
              <label key={r} className="check">
                <input type="checkbox" checked={roles.includes(r)} disabled={roles.length === 1 && roles[0] === r} onChange={() => toggle(r)} />{r}
              </label>
            ))}
          </div>
          {command ? <Cmd text={command} /> : <div className="skel-row"><i className="skel w50" /></div>}
          <p className="help">The token expires in 1 hour and works once. Name defaults to the machine's hostname (<span className="mono">--name</span> to change it). Then start the agent with <span className="mono">walroos node run</span>.</p>
          {joined ? (
            <div className="alert alert-ok"><Icon n="check" /><div className="alert-text"><strong><span className="mono">{joined.name}</span> joined</strong><span className="muted">Telemetry shows up once <span className="mono">walroos node run</span> is up.</span></div></div>
          ) : (
            <div className="alert" aria-live="polite"><Icon n="refresh" cls="spin" /><div className="alert-text"><strong>Waiting for node to join…</strong><span className="muted">Keep this open, or close it: the token stays valid.</span></div></div>
          )}
        </>
      )}
    </Modal>
  );
}
