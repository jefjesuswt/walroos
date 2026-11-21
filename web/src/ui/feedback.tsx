// Feedback: status badges, alerts, empty and loading states, toasts.
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { tone } from '../status';
import { Icon } from './icons';

export const Badge = ({ s, label = s }: { s: string; label?: string }) => (
  <span className={`badge s-${tone(s)}`}><span className="dot" aria-hidden="true" />{label}</span>
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

// Alert is a banner about the thing on the page (its node is down, it's missing): not a form error.
export const Alert = ({ title, tone = 'bad', children }: { title: ReactNode; tone?: 'bad' | 'busy'; children: ReactNode }) => (
  <div className={`alert alert-${tone}`} role={tone === 'bad' ? 'alert' : 'status'}><Icon n="alert" />
    <div className="alert-text"><strong>{title}</strong><span className="muted">{children}</span></div>
  </div>
);

// Empty states show the mascot; only problems get an icon.
export const EmptyState = ({ icon, title, text, action }: { icon: string; title: string; text: string; action?: ReactNode }) => (
  <div className="state">
    {icon === 'alert' ? <Icon n="alert" cls="ic-lg" /> : <img className="mascot" src="/mascot.svg" alt="" width={112} height={112} />}
    <h3>{title}</h3><p className="muted">{text}</p>{action}
  </div>
);

// Loading is the skeleton a page or a card shows until its data arrives: rows of bars, as wide
// (in %) as the columns that will replace them.
export const Loading = ({ rows = 1, cols = [30, 50] }: { rows?: number; cols?: number[] }) => (
  <div className="skel-list" aria-busy="true" aria-label="Loading">
    {Array.from({ length: rows }, (_, i) => <div key={i} className="skel-row">{cols.map(w => <i key={w} className={`skel w${w}`} />)}</div>)}
  </div>
);

// LoadFailed is a whole page that couldn't load its subject, with a way back.
export const LoadFailed = ({ what, error, notFound, back, backLabel }: {
  what: string; error: string; notFound: boolean; back: string; backLabel: string;
}) => (
  <EmptyState icon="alert" title={notFound ? `No such ${what}` : `Couldn't load the ${what}`} text={error}
    action={<a className="btn" href={back}>{backLabel}</a>} />
);

// LoadError is a list that couldn't load, with Retry.
export const LoadError = ({ what, error, onRetry }: { what: string; error: string; onRetry: () => void }) => (
  <div className="state state-err" role="alert">
    <Icon n="alert" cls="ic-lg" /><h3>Couldn't load {what}</h3><p className="muted">{error}</p>
    <button className="btn" onClick={onRetry}><Icon n="refresh" />Retry</button>
  </div>
);

type Toast = { id: number; text: string; kind: 'ok' | 'bad' };
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
