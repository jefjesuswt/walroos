// Small components ported 1:1 from the Open Design prototype (index.html at the repo root).
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { api, type EnvVar, type Project, type User } from './api';

const P: Record<string, string> = {
  overview: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  workspace: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M13 15h4"/>',
  apps: '<path d="M12 3 3 7.5l9 4.5 9-4.5L12 3Z"/><path d="m3 12 9 4.5 9-4.5M3 16.5 12 21l9-4.5"/>',
  deploy: '<circle cx="12" cy="12" r="9"/><path d="m8 12 4-4 4 4M12 8v8"/>',
  nodes: '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/>',
  plug: '<path d="M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0V7ZM12 17v5"/>',
  settings: '<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/>',
  check: '<path d="m5 12 5 5L20 7"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
  refresh: '<path d="M20 11A8 8 0 0 0 5.6 6.6L3 9M3 4v5h5M4 13a8 8 0 0 0 14.4 4.4L21 15M21 20v-5h-5"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  alert: '<path d="M12 3 2 20h20L12 3Z"/><path d="M12 10v4M12 17h.01"/>',
  palette: '<path d="M12 3a9 9 0 1 0 0 18c1.1 0 1.5-.8 1.5-1.5 0-.9-.7-1.2-.7-2 0-.8.7-1.5 1.5-1.5H17a4 4 0 0 0 4-4c0-5-4-9-9-9Z"/><circle cx="7.5" cy="11" r="1"/><circle cx="10.5" cy="7" r="1"/><circle cx="15" cy="7.5" r="1"/>',
  upload: '<path d="M12 16V4M7 9l5-5 5 5M4 20h16"/>',
  rollback: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 0 12h-3"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>',
  database: '<ellipse cx="12" cy="5.5" rx="8" ry="3"/><path d="M4 5.5v13c0 1.7 3.6 3 8 3s8-1.3 8-3v-13M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  download: '<path d="M12 4v12M7 11l5 5 5-5M4 20h16"/>',
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>',
  more: '<circle cx="12" cy="5" r="1.2"/><circle cx="12" cy="12" r="1.2"/><circle cx="12" cy="19" r="1.2"/>',
};

export const Icon = ({ n, cls = '' }: { n: string; cls?: string }) => (
  <svg className={`ic ${cls}`} viewBox="0 0 24 24" aria-hidden="true" dangerouslySetInnerHTML={{ __html: P[n] || '' }} />
);

export const Logo = () => <img src="/favicon.svg" alt="" width={24} height={24} />;

const ST: Record<string, string> = {
  online: 'ok', running: 'ok', succeeded: 'ok', active: 'ok', pending: 'busy', creating: 'busy', deleting: 'busy', deploying: 'busy', activating: 'busy',
  offline: 'bad', failed: 'bad', missing: 'bad', stopped: 'idle', queued: 'idle', inactive: 'idle',
};
export const Badge = ({ s, label = s }: { s: string; label?: string }) => (
  <span className={`badge s-${ST[s] || 'idle'}`}><span className="dot" aria-hidden="true" />{label}</span>
);

// ErrorAlert is a form's error. It scrolls itself into view when it appears or changes: in a tall
// modal (a bottom sheet on phones) the submit button is at the bottom and the alert at the top.
export function ErrorAlert({ title, msg }: { title: string; msg: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    ref.current?.scrollIntoView({ block: 'nearest', behavior: reduce ? 'auto' : 'smooth' });
  }, [title, msg]);
  return (
    <div ref={ref} className="alert alert-bad" role="alert"><Icon n="alert" />
      <div className="alert-text"><strong>{title}</strong><span className="muted">{msg}</span></div>
    </div>
  );
}

export const Chip = ({ children }: { children: ReactNode }) => <span className="chip">{children}</span>;

export function Meter({ label, pct, text }: { label: string; pct: number; text: string }) {
  const p = Math.max(0, Math.min(100, Math.round(pct || 0)));
  const lvl = p >= 85 ? 'b-bad' : p >= 70 ? 'b-busy' : '';
  return (
    <div className="meter">
      <div className="meter-h"><span>{label}</span><span className="mono od-nowrap">{text}</span></div>
      <div className="bar" role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={p}><i className={lvl} style={{ width: `${p}%` }} /></div>
    </div>
  );
}

/* ---------- toasts + copy ---------- */
type Toast = { id: number; text: string; kind: 'ok' | 'bad' };
// Who is using the hub, and what their role lets them do. Mirrors internal/auth/permissions.go:
// the server refuses anyway; the UI just doesn't offer what would only answer 403.
export type Perm = 'apps' | 'databases' | 'nodes' | 'settings' | 'users' | 'any-workspace';
const GRANTS: Record<string, Perm[]> = {
  owner: ['apps', 'databases', 'nodes', 'settings', 'users', 'any-workspace'],
  deployer: ['apps', 'databases'],
};
export const allowed = (me: User | null, p: Perm) => !!me && (GRANTS[me.role] ?? []).includes(p);
export const MeCtx = createContext<User | null>(null);
export function useCan() {
  const me = useContext(MeCtx);
  return {
    me,
    can: (p: Perm) => allowed(me, p),
    // their own workspace, or a role that may touch anyone's
    canUse: (owner: string) => !!me && (me.login === owner || allowed(me, 'any-workspace')),
  };
}

const ToastCtx = createContext<(text: string, kind?: 'ok' | 'bad') => void>(() => {});
export const useToast = () => useContext(ToastCtx);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = (text: string, kind: 'ok' | 'bad' = 'ok') => {
    const id = Date.now() + Math.random();
    setToasts(t => [...t, { id, text, kind }]);
    setTimeout(() => setToasts(t => t.filter(x => x.id !== id)), 3200);
  };
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div id="toasts" role="status" aria-live="polite">
        {toasts.map(t => <div key={t.id} className={`toast ${t.kind}`}><Icon n={t.kind === 'bad' ? 'alert' : 'check'} /><span>{t.text}</span></div>)}
      </div>
    </ToastCtx.Provider>
  );
}

// navigator.clipboard needs a secure context; http://walroos-hub on the tailnet isn't one.
function copyText(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  const ok = document.execCommand('copy');
  ta.remove();
  return ok ? Promise.resolve() : Promise.reject(new Error('copy failed'));
}

export function CopyBtn({ text, label = 'Copy' }: { text: string; label?: string }) {
  const toast = useToast();
  const [done, setDone] = useState(false);
  const onClick = () => copyText(text).then(
    () => { toast('Copied to clipboard'); setDone(true); setTimeout(() => setDone(false), 1200); },
    () => toast('Copy failed: select the text and copy it', 'bad'),
  );
  return (
    <button type="button" className={`btn-icon${done ? ' copied' : ''}`} aria-label={label} title={label} onClick={onClick}>
      <Icon n={done ? 'check' : 'copy'} />
    </button>
  );
}

export const Cmd = ({ text }: { text: string }) => (
  <div className="cmd"><span className="prompt" aria-hidden="true">$</span><code>{text}</code><CopyBtn text={text} label="Copy command" /></div>
);

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

// Field is label + control + hint, the block every form in the app repeats.
export function Field({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="field">
      <span className="label">{label}</span>
      {children}
      {hint && <span className="help">{hint}</span>}
    </label>
  );
}

// Empty states show the mascot; only problems get an icon.
export const EmptyState = ({ icon, title, text, action }: { icon: string; title: string; text: string; action?: ReactNode }) => (
  <div className="state">
    {icon === 'alert' ? <Icon n="alert" cls="ic-lg" /> : <img className="mascot" src="/mascot.svg" alt="" width={112} height={112} />}
    <h3>{title}</h3><p className="muted">{text}</p>{action}
  </div>
);

export function ago(iso: string | null): string {
  if (!iso) return 'never';
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 10) return 'just now';
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** Calls fn now and every ms while mounted; pauses when the tab is hidden. */
export function usePoll(fn: () => void, ms: number) {
  const saved = useRef(fn);
  saved.current = fn;
  useEffect(() => {
    saved.current();
    const id = setInterval(() => !document.hidden && saved.current(), ms);
    return () => clearInterval(id);
  }, [ms]);
}

// EnvModal edits a workspace's or an app's variables; the last save replaces the whole set.
export function EnvModal({ title, sub, savedMsg, load, save, onClose, onSaved }: {
  title: string; sub: string; savedMsg: string; load: () => Promise<EnvVar[]>; save: (vars: EnvVar[]) => Promise<unknown>;
  onClose: () => void; onSaved: () => void;
}) {
  const toast = useToast();
  const [vars, setVars] = useState<EnvVar[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => { load().then(setVars, e => setError(e.message)); }, []); // once: a poll would overwrite what you're typing

  const submit = () => {
    setBusy(true);
    save(vars ?? []).then(
      () => { toast(savedMsg); onSaved(); },
      e => { setError(e.message); setBusy(false); },
    );
  };

  return (
    <Modal title={title} wide onClose={onClose} sub={sub}
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={busy || !vars} onClick={submit}>{busy ? 'Saving…' : 'Save'}</button>
      </>}>
      {error && <ErrorAlert title="Couldn't load or save" msg={error} />}
      {!vars ? <div className="skel-row"><i className="skel w50" /></div> : <EnvRows vars={vars} setVars={setVars} />}
    </Modal>
  );
}

function EnvRows({ vars, setVars }: { vars: EnvVar[]; setVars: (f: (vs: EnvVar[] | null) => EnvVar[]) => void }) {
  const set = (i: number, patch: Partial<EnvVar>) => setVars(vs => (vs ?? []).map((v, j) => j === i ? { ...v, ...patch } : v));
  return (
    <div className="form">
      {vars.length === 0 && <p className="muted">No variables yet.</p>}
      {vars.map((v, i) => (
        <div key={i} className="env-row">
          <input className="input mono" value={v.key} placeholder="KEY" aria-label="Name"
            onChange={e => set(i, { key: e.target.value.toUpperCase() })} />
          <input className="input mono" value={v.value} placeholder="value" aria-label="Value"
            type={v.secret ? 'password' : 'text'} onChange={e => set(i, { value: e.target.value })} />
          <label className="check" title="Mask this value in the UI">
            <input type="checkbox" checked={v.secret} onChange={e => set(i, { secret: e.target.checked })} />secret
          </label>
          <button className="btn-icon" aria-label="Remove" title="Remove" onClick={() => setVars(vs => (vs ?? []).filter((_, j) => j !== i))}><Icon n="trash" /></button>
        </div>
      ))}
      <div>
        <button className="btn" onClick={() => setVars(vs => [...(vs ?? []), { key: '', value: '', secret: false }])}><Icon n="plus" />Add variable</button>
      </div>
      <p className="help">Names: A–Z, 0–9 and _, starting with a letter or _. The last save replaces the whole set.</p>
    </div>
  );
}

// EnvEditor is EnvModal's editor in a page (a service's Environment tab).
export function EnvEditor({ load, save, savedMsg }: { load: () => Promise<EnvVar[]>; save: (vars: EnvVar[]) => Promise<unknown>; savedMsg: string }) {
  const toast = useToast();
  const [vars, setVars] = useState<EnvVar[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { load().then(setVars, e => setError(e.message)); }, []); // once: a poll would overwrite what you're typing
  const submit = () => {
    setBusy(true);
    save(vars ?? []).then(() => { toast(savedMsg); setBusy(false); setError(''); }, e => { setError(e.message); setBusy(false); });
  };
  return (
    <>
      {error && <ErrorAlert title="Couldn't load or save" msg={error} />}
      {!vars ? <div className="skel-row"><i className="skel w50" /></div> : <EnvRows vars={vars} setVars={setVars} />}
      <div><button className="btn btn-primary" disabled={busy || !vars} onClick={submit}><Icon n="check" />{busy ? 'Saving…' : 'Save'}</button></div>
    </>
  );
}

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

// A page that sits inside another (an app in its project) tells the top bar its parents.
export type Crumb = { label: string; href: string };
export const TrailCtx = createContext<(t: Crumb[]) => void>(() => {});
export function useTrail(t: Crumb[] | null) {
  const set = useContext(TrailCtx);
  const key = JSON.stringify(t);
  useEffect(() => { if (t) set(t); return () => set([]); }, [key]);
}

// ProjectField picks the project a new service goes in. Locked when the dialog was opened from a
// project's page.
export function ProjectField({ value, onChange, locked }: { value: string; onChange: (v: string) => void; locked?: boolean }) {
  const [list, setList] = useState<Project[]>([]);
  useEffect(() => { if (!locked) api.projects().then(setList, () => undefined); }, [locked]);
  if (locked) return null; // opened from the project's page: it's already chosen
  return (
    <Field label="Project" hint="Services of a project reach each other by name, on their own network. Nothing else can reach them.">
      <select className="input" value={value} onChange={e => onChange(e.target.value)}>
        {list.map(p => <option key={p.id} value={p.name === 'default' ? '' : p.name}>{p.name}</option>)}
      </select>
    </Field>
  );
}
