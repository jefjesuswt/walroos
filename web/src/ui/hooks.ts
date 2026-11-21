// Hooks and helpers every page uses: polling, relative times, the top bar's breadcrumbs.
import { createContext, useContext, useEffect, useRef } from 'react';

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

// A page that sits inside another (an app in its project) tells the top bar its parents.
export type Crumb = { label: string; href: string };
export const TrailCtx = createContext<(t: Crumb[]) => void>(() => {});
export function useTrail(t: Crumb[] | null) {
  const set = useContext(TrailCtx);
  const key = JSON.stringify(t);
  useEffect(() => { if (t) set(t); return () => set([]); }, [key]);
}
