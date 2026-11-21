import type { App, Deployment } from '../api';
import { duration, isCurrent, settling, short } from '../status';
import { ago, Badge } from '../ui';

// DeploymentsTable lists deployments, newest first. With app it's that app's history (and marks
// the live release); without, every app's, with a column for the app (and no column that grows:
// app names and commits share the width).
export function DeploymentsTable({ list, app, onOpen }: { list: Deployment[]; app?: App; onOpen: (d: Deployment) => void }) {
  const pulled = !!app?.image;
  return (
    <div className="table-wrap">
      <table className="tbl">
        <thead><tr><th>State</th>{!app && <th>App</th>}<th className={app ? 'col-grow' : undefined}>{pulled ? 'Image' : 'Commit'}</th><th className="hide-sm">Duration</th><th className="hide-xs">When</th></tr></thead>
        <tbody>
          {list.map(d => (
            <tr key={d.id} className="row-link" onClick={() => onOpen(d)}>
              <td><Badge s={d.state} /></td>
              {!app && <td><a className="cell-title mono" href={`#/apps/${d.app_name}`} onClick={e => e.stopPropagation()}>{d.app_name}</a></td>}
              <td className={app ? 'col-grow' : undefined}><div className="cell-stack">
                <span className="od-row">
                  <button className="sha link-btn" aria-label={`Log of deployment ${d.id}`}>{short(d.sha) || (pulled ? 'pull' : 'branch head')}</button>
                  {app && isCurrent(app, d) && <span className="tag-current">current</span>}
                </span>
                {d.state === 'failed' && d.error && <span className={app ? 'cell-sub err-line' : 'cell-sub od-truncate err-line err-wide'} title={d.error}>{d.error}</span>}
              </div></td>
              <td className="hide-sm mono">{settling(d) ? '…' : duration(d)}</td>
              <td className="hide-xs muted od-nowrap">{ago(d.created_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
