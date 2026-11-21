import { useCallback, useState } from 'react';
import { api, ROLES, type JoinToken, type Node, type Role } from '../api';
import { ago, Alert, Badge, Check, Chip, Cmd, Confirm, EmptyState, ErrorAlert, Foot, Icon, Loading, LoadError, Meter, Modal, PageBar, RowActions, useCan, usePoll, useToast } from '../ui';

export function NodesPage({ addOpen, setAddOpen }: { addOpen: boolean; setAddOpen: (v: boolean) => void }) {
  const toast = useToast();
  const { can } = useCan();
  const [nodes, setNodes] = useState<Node[] | null>(null);
  const [error, setError] = useState('');
  const [removing, setRemoving] = useState<Node | null>(null);
  const [editing, setEditing] = useState<Node | null>(null);

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
        <PageBar sub={`${online} of ${nodes.length} online`} />
      )}

      {error && !nodes ? (
<LoadError what="nodes" error={error} onRetry={load} />
      ) : !nodes ? (
<Loading rows={3} cols={[30, 50, 15]} />
      ) : nodes.length === 0 ? (
        <EmptyState icon="nodes" title="No nodes yet"
          text="Join a machine to the hub over Tailscale. One command, then it shows up here."
          action={can('nodes') ? <button className="btn btn-primary" onClick={() => setAddOpen(true)}><Icon n="plus" />Add node</button> : undefined} />
      ) : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr>
              <th className="col-grow">Node</th><th className="hide-sm">Roles</th><th>Status</th>
              <th className="hide-sm col-meter-sm">CPU</th><th className="hide-sm col-meter">RAM</th>
              <th className="hide-md col-meter">Disk</th><th className="hide-md">Podman</th>
              <th className="hide-sm">Last seen</th><th><span className="sr-only">Actions</span></th>
            </tr></thead>
            <tbody>{nodes.map(n => <NodeRow key={n.id} n={n} onRemove={() => setRemoving(n)} onRoles={() => setEditing(n)} />)}</tbody>
          </table>
        </div>
      )}

      {editing && <RolesModal n={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); load(); }} />}
      {addOpen && <AddNodeModal known={nodes ?? []} onClose={() => setAddOpen(false)} onJoined={load} />}

      {removing && (
        <Confirm title={`Remove ${removing.name}?`} confirmLabel="Remove node" danger onClose={() => setRemoving(null)} onConfirm={() => remove(removing)}>
          <p className="muted">Its token stops working and the agent exits on the next heartbeat. Containers on the machine keep running. To bring it back, join it again with a new token.</p>
        </Confirm>
      )}
    </>
  );
}

function NodeRow({ n, onRemove, onRoles }: { n: Node; onRemove: () => void; onRoles: () => void }) {
  const { can } = useCan();
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
      <td className="hide-sm roles-cell"><div className="od-cluster gap-1">{n.roles.map(r => <Chip key={r}>{r}</Chip>)}</div></td>
      <td><Badge s={n.status} /></td>
      <td className="hide-sm">{live ? <Meter label="CPU" pct={t.cpu_percent} text={`${Math.round(t.cpu_percent)}%`} /> : dash}</td>
      <td className="hide-sm">{live && t.mem_total_mb ? <Meter label="RAM" pct={t.mem_used_mb / t.mem_total_mb * 100} text={`${gb(t.mem_used_mb)}/${gb(t.mem_total_mb)} GB`} /> : dash}</td>
      <td className="hide-md">{live && t.disk_total_gb ? <Meter label="Disk" pct={t.disk_used_gb / t.disk_total_gb * 100} text={`${t.disk_used_gb}/${t.disk_total_gb} GB`} /> : dash}</td>
      <td className="hide-md">{t.podman ? <div className="cell-stack"><span className="mono">{t.podman}</span>
        <span className="cell-sub od-nowrap">{t.containers} containers</span></div> : dash}</td>
      <td className={`hide-sm od-nowrap${n.status === 'offline' ? ' bad-tx' : ' muted'}`}>{ago(n.last_seen)}</td>
      <td className="ta-r od-nowrap">{can('nodes') && <RowActions name={n.name} actions={[
        { icon: 'settings', label: 'Roles', run: onRoles },
        { icon: 'trash', label: 'Remove node', run: onRemove },
      ]} />}</td>
    </tr>
  );
}

// RoleChecks picks a node's roles: at least one, kept in ROLES order.
function RoleChecks({ roles, setRoles, help }: { roles: Role[]; setRoles: (f: (rs: Role[]) => Role[]) => void; help?: boolean }) {
  const toggle = (r: Role) => setRoles(rs => rs.includes(r) ? rs.filter(x => x !== r) : ROLES.filter(x => x === r || rs.includes(x)));
  return <>
    {ROLES.map(r => (
      <Check key={r} checked={roles.includes(r)} disabled={roles.length === 1 && roles[0] === r} onChange={() => toggle(r)}>
        {help ? <span><span className="mono">{r}</span> <span className="muted">· {ROLE_HELP[r]}</span></span> : r}
      </Check>
    ))}
  </>;
}

const ROLE_HELP: Record<Role, string> = {
  dev: 'Runs workspaces.',
  build: 'Builds app images for prod nodes.',
  prod: 'Runs apps and databases.',
};

function RolesModal({ n, onClose, onSaved }: { n: Node; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [roles, setRoles] = useState<Role[]>(n.roles);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const dropped = n.roles.filter(r => !roles.includes(r));
  const save = () => {
    setBusy(true);
    api.setNodeRoles(n.id, roles).then(() => { toast(`${n.name}: ${roles.join(', ')}`); onSaved(); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <Modal title={`Roles · ${n.name}`} onClose={onClose} sub="Roles decide where new workspaces, builds, apps and databases go."
      foot={<Foot onCancel={onClose} onClick={save} busy={busy} disabled={roles.length === 0 || roles.join() === n.roles.join()} label="Save" busyLabel="Saving…" />}>
      {error && <ErrorAlert title="Couldn't change them" msg={error} />}
      <div className="form">
        <RoleChecks roles={roles} setRoles={setRoles} help />
        {dropped.length > 0 && <p className="help">Without {dropped.join(' and ')}, what already runs on {n.name} keeps running; only new ones go elsewhere.</p>}
      </div>
    </Modal>
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

  const command = jt && `${jt.command}${roles.join(',') === 'dev' ? '' : ` --roles ${roles.join(',')}`}`;

  return (
    <Modal title="Add node" wide onClose={onClose}
      sub="Run this on the machine you want to join. It needs Tailscale up; Podman for workspaces and apps.">
      {error ? (
        <Alert title="Couldn't create a join token">{error}</Alert>
      ) : (
        <>
          <div className="od-cluster gap-4" role="group" aria-label="Roles">
            <span className="label">Roles</span>
            <RoleChecks roles={roles} setRoles={setRoles} />
          </div>
          {command ? <Cmd text={command} /> : <Loading cols={[50]} />}
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
