// The tabs of a database's page.
import { api, type Backup, type Database } from '../api';
import { dbLabel, size } from '../status';
import { ago, CopyField, DangerZone, ErrorAlert, Icon, Section } from '../ui';

const schedule = (d: Database) => d.backup_every_hours === 0 ? 'off'
  : d.backup_every_hours % 24 === 0 ? `every ${d.backup_every_hours / 24 === 1 ? 'day' : `${d.backup_every_hours / 24} days`}`
  : `every ${d.backup_every_hours} h`;

export function ConnectionCard({ d }: { d: Database }) {
  const url = d.url ?? '';
  const masked = url.replace(/:([^:@/]+)@/, ':••••••••@');
  return (
    <Section id="conn-h" title="Connection"
      sub={<>From the services of <a href={`#/projects/${d.project}`}>{d.project}</a>, by name. Nothing outside the project can reach it.</>}>
      <dl className="kv">
        <dt>Host</dt><dd className="mono">{d.name} <span className="muted">(or {d.host})</span></dd>
        <dt>Port</dt><dd className="mono">{d.port}</dd>
        {d.engine !== 'redis' && <><dt>User</dt><dd className="mono">{d.user}</dd><dt>Database</dt><dd className="mono">{d.db}</dd></>}
      </dl>
      {url && <CopyField value={url} masked={masked} label="connection URL" />}
      <p className="help"><strong>Link to app</strong> puts this URL in an app's environment as a secret; it applies on the app's next deploy.</p>
    </Section>
  );
}

export function BackupsTab({ d, backups, onSchedule, onRestore }: {
  d: Database; backups: Backup[] | null; onSchedule: () => void; onRestore: (b: Backup) => void;
}) {
  return (
    <Section id="bk-h" title="Backups"
      sub={<>Gzipped dumps on {d.node_name}, in <span className="mono">~/walroos-backups/{d.name}</span>. {schedule(d) === 'off' ? 'Scheduled backups are off.' : `Scheduled ${schedule(d)}, keeping ${d.backup_keep}.`}</>}
      actions={<button className="btn" onClick={onSchedule}><Icon n="settings" />Schedule</button>}>
      {d.last_backup_error && <ErrorAlert title={`The last backup failed (${ago(d.last_backup_at ?? null)})`} msg={d.last_backup_error} />}
      {!backups ? <p className="muted">Loading…</p> : backups.length === 0 ? <p className="muted">No backups yet. <strong>Back up now</strong> makes the first one.</p> : (
        <div className="table-wrap">
          <table className="tbl">
            <thead><tr><th className="col-grow">File</th><th className="hide-xs">Size</th><th className="hide-sm">Taken</th><th><span className="sr-only">Actions</span></th></tr></thead>
            <tbody>
              {backups.map(b => (
                <tr key={b.file}>
                  <td className="col-grow"><div className="cell-stack"><span className="cell-title mono" title={b.file}>{b.file}</span>
                    <span className="cell-sub show-xs-inline">{size(b.size)} · {ago(b.created_at)}</span></div></td>
                  <td className="hide-xs mono od-nowrap">{size(b.size)}</td>
                  <td className="hide-sm muted od-nowrap">{ago(b.created_at)}</td>
                  <td className="ta-r od-nowrap">
                    <a className="btn-icon" href={api.backupURL(d.name, b.file)} download={b.file} aria-label={`Download ${b.file}`} title="Download"><Icon n="download" /></a>
                    <button className="btn-icon" aria-label={`Restore ${b.file}`} title="Restore" disabled={d.node_down} onClick={() => onRestore(b)}><Icon n="rollback" /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="help">They live on the same disk as the database: download one now and then to keep a copy elsewhere.</p>
    </Section>
  );
}

export function AdvancedTab({ d, onDelete }: { d: Database; onDelete: () => void }) {
  return <>
    <Section id="dbrun-h" title="Runtime" sub={`What runs on ${d.node_name}.`}>
      <dl className="kv">
        <dt>Container</dt><dd className="mono">{d.host}</dd>
        <dt>Data</dt><dd className="mono">podman volume {d.host}</dd>
        <dt>Engine</dt><dd>{dbLabel(d)}</dd>
        <dt>Limits</dt><dd>2 GB of memory, 2048 processes</dd>
      </dl>
    </Section>
    <DangerZone id="dbdel-h" title="Delete database" sub={`Stops it on ${d.node_name}. Apps using it lose their connection.`} onDelete={onDelete} />
  </>;
}
