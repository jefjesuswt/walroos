// Things that float over the page: dialogs and menus.
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Icon } from './icons';

/* ---------- modal ---------- */
// Modal owns the single close affordance: the X in the head, always. The footer exists only for
// actions (Cancel + something), so content-only modals have no second way to close.
export function Modal({ title, sub, wide, onClose, children, foot }: {
  title: string; sub?: string; wide?: boolean; onClose: () => void; children: ReactNode; foot?: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => { // once per open: callers pass inline onClose, re-running would steal focus on every render
    const last = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>('[autofocus], input, button')?.focus();
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close.current();
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('keydown', onKey); last?.focus(); };
  }, []);
  // portal: a modal inside .content would sit under sticky/scrolling table layers
  return createPortal(
    <div className="overlay ov-modal" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div ref={ref} className={`modal${wide ? ' wide' : ''}`} role="dialog" aria-modal="true" aria-labelledby="m-title">
        <div className="m-head">
          <div><h2 id="m-title">{title}</h2>{sub && <p className="muted">{sub}</p>}</div>
          <button type="button" className="btn-icon" aria-label="Close" onClick={onClose}><Icon n="x" /></button>
        </div>
        <div className="m-body">{children}</div>
        {foot && <div className="m-foot">{foot}</div>}
      </div>
    </div>,
    document.body,
  );
}

// Confirm is a Modal with Cancel and one action: the shape every destructive question repeats.
export function Confirm({ title, confirmLabel, danger, onConfirm, onClose, children }: {
  title: string; confirmLabel: string; danger?: boolean; onConfirm: () => void; onClose: () => void; children: ReactNode;
}) {
  return (
    <Modal title={title} onClose={onClose}
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className={danger ? 'btn btn-danger' : 'btn btn-primary'} autoFocus onClick={onConfirm}>{confirmLabel}</button>
      </>}>
      {children}
    </Modal>
  );
}

export type MenuItem = { icon: string; title: string; text: string; onClick?: () => void; disabled?: boolean };

// PopMenu is a button that opens a list of choices under it (Create service → App, Database…).
// Pass open/setOpen to open it from elsewhere too (an empty state's button).
export function PopMenu({ label, icon = 'plus', items, open: given, setOpen: setGiven }: {
  label: string; icon?: string; items: MenuItem[]; open?: boolean; setOpen?: (v: boolean) => void;
}) {
  const [own, setOwn] = useState(false);
  const open = given ?? own;
  const setOpen = setGiven ?? setOwn;
  return (
    <div className="popmenu-wrap">
      <button className="btn btn-primary" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}><Icon n={icon} />{label}</button>
      {open && <>
        <div className="popmenu-scrim" onClick={() => setOpen(false)} />
        <div className="popmenu" role="menu">
          {items.map(it => (
            <button key={it.title} role="menuitem" disabled={it.disabled} onClick={() => { setOpen(false); it.onClick?.(); }}>
              <Icon n={it.icon} /><span><strong>{it.title}</strong><span className="help">{it.text}</span></span>
            </button>
          ))}
        </div>
      </>}
    </div>
  );
}

// Foot is a dialog's Cancel + main action, the footer every form dialog repeats. busyLabel shows
// while the action runs.
export function Foot({ onCancel, label, busyLabel, busy, icon, disabled, danger, onClick }: {
  onCancel: () => void; label: string; busyLabel?: string; busy?: boolean; icon?: string; disabled?: boolean; danger?: boolean; onClick: () => void;
}) {
  return (
    <>
      <button className="btn" onClick={onCancel}>Cancel</button>
      <button className={danger ? 'btn btn-danger' : 'btn btn-primary'} disabled={busy || disabled} onClick={onClick}>
        {icon && <Icon n={icon} />}{busy && busyLabel ? busyLabel : label}
      </button>
    </>
  );
}

// An Action is one thing a table row can do. Wide screens show them as icon buttons (RowActions);
// phones show a ⋮ that opens them in a sheet (ActionSheet).
export type Action = { icon: string; label: string; title?: string; run: () => void; off?: boolean; danger?: boolean };

export const RowActions = ({ name, actions }: { name: string; actions: Action[] }) => <>
  {actions.map(a => (
    <button key={a.label} className="btn-icon" title={a.title ?? a.label} aria-label={`${a.label}: ${name}`} disabled={a.off} onClick={a.run}><Icon n={a.icon} /></button>
  ))}
</>;

export function ActionSheet({ title, sub, actions, onClose }: { title: string; sub?: string; actions: Action[]; onClose: () => void }) {
  return (
    <Modal title={title} sub={sub} onClose={onClose}>
      <div className="sheet-list">
        {actions.map(a => (
          <button key={a.label} className={a.danger ? 'danger' : ''} disabled={a.off} onClick={() => { onClose(); a.run(); }}><Icon n={a.icon} />{a.label}</button>
        ))}
      </div>
    </Modal>
  );
}
