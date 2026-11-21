// Page structure: the header of a page, its sections (cards) and tabs.
import type { ReactNode } from 'react';
import { Icon } from './icons';

// PageHeader is the top of a resource's page: its mark, name, status, a line of facts and the
// main actions.
export function PageHeader({ mark, title, mono, badge, meta, actions }: {
  mark?: ReactNode; title: string; mono?: boolean; badge?: ReactNode; meta?: ReactNode; actions?: ReactNode;
}) {
  return (
    <div className="page-head">
      <div className="ph-text">
        <div className="title-row">{mark && <span className="svc-logo" aria-hidden="true">{mark}</span>}<h1 className={mono ? 'mono' : undefined}>{title}</h1>{badge}</div>
        {meta && <div className="ph-meta">{meta}</div>}
      </div>
      {actions && <div className="ph-actions">{actions}</div>}
    </div>
  );
}

// PageBar is the line under a list page's title: a summary and the page's actions.
export const PageBar = ({ sub, children }: { sub: ReactNode; children?: ReactNode }) => (
  <div className="page-head page-bar"><p className="muted page-sub">{sub}</p>{children ?? <span />}</div>
);

// Section is a card with a head (title, a line on what it is, actions) and a body. form lays the
// body out as a form; flush drops the body padding (a table that runs edge to edge).
export function Section({ id, title, count, sub, actions, form, flush, children }: {
  id: string; title: ReactNode; count?: ReactNode; sub?: ReactNode; actions?: ReactNode; form?: boolean; flush?: boolean; children?: ReactNode;
}) {
  return (
    <section className="card" aria-labelledby={id}>
      <div className="card-head">
        <div><h2 id={id}>{title}{count !== undefined && <span className="count">{count}</span>}</h2>{sub && <p className="muted">{sub}</p>}</div>
        {actions}
      </div>
      {children !== undefined && (flush ? children : <div className={form ? 'card-body form' : 'card-body'}>{children}</div>)}
    </section>
  );
}

// Note is a card holding one line of text (what your role can't see, say).
export const Note = ({ children }: { children: ReactNode }) => <div className="card card-pad muted">{children}</div>;

// Tabs switches the sections of a page (a service: General, Environment, Domains…).
export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: [T, string][]; value: T; onChange: (t: T) => void }) {
  return (
    <nav className="tabs" role="tablist">
      {tabs.map(([id, label]) => (
        <button key={id} role="tab" aria-selected={id === value} aria-current={id === value ? 'page' : undefined} onClick={() => onChange(id)}>{label}</button>
      ))}
    </nav>
  );
}

// DangerZone is the last card of a resource's settings: delete it.
export const DangerZone = ({ id, title, sub, onDelete }: { id: string; title: string; sub: string; onDelete: () => void }) => (
  <section className="card danger-zone" aria-labelledby={id}>
    <div className="card-head">
      <div><h2 id={id}>{title}</h2><p className="muted">{sub}</p></div>
      <button className="btn btn-danger" onClick={onDelete}><Icon n="trash" />Delete</button>
    </div>
  </section>
);
