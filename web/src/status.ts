// What each resource is doing, in one place: every page, list and badge reads states and labels
// from here so a service never shows one state in its project and another on its own page.
import type { App, Database, Deployment } from './api';

export type State = [tone: string, label: string];

// The badge color of a raw state.
const TONE: Record<string, string> = {
  online: 'ok', running: 'ok', succeeded: 'ok', active: 'ok',
  pending: 'busy', creating: 'busy', deleting: 'busy', deploying: 'busy', activating: 'busy',
  offline: 'bad', failed: 'bad', missing: 'bad',
  stopped: 'idle', queued: 'idle', inactive: 'idle',
};
export const tone = (s: string) => TONE[s] ?? 'idle';

/* ---------- apps ---------- */
export const isDraft = (a: App) => !a.repo_url && !a.image;
export const settling = (d?: Deployment) => d?.state === 'queued' || d?.state === 'running';
export const short = (sha: string) => sha.slice(0, 7);
export const isCurrent = (a: App, d: Deployment) => a.running?.container === `${a.name}-${d.id}`;

// What the app is doing, from the node's live release first: a deployment's state is only
// what the hub saw (it can say failed for a deploy the node finished after a hub restart).
export function appState(a: App): State {
  if (a.node_down) return ['offline', 'node offline'];
  if (isDraft(a)) return ['stopped', 'no source yet'];
  const d = a.last_deployment;
  if (d && settling(d)) return ['deploying', d.state === 'queued' ? 'queued' : 'deploying'];
  if (a.running) return a.running.state === 'active' ? ['running', 'running'] : [a.running.state, a.running.state];
  if (d?.state === 'failed') return ['failed', 'deploy failed'];
  return ['stopped', 'not deployed'];
}

export function duration(d: Deployment, now = Date.now()): string {
  const end = d.finished_at ? new Date(d.finished_at).getTime() : now;
  const s = Math.max(0, Math.round((end - new Date(d.created_at).getTime()) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

/* ---------- databases ---------- */
const ENGINE: Record<string, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', redis: 'Redis', valkey: 'Valkey', mongo: 'MongoDB' };
export const engineLabel = (engine: string) => ENGINE[engine] ?? engine;
export const dbLabel = (d: Database) => `${engineLabel(d.engine)} ${d.version}`;
// The Simple Icons slug of an engine; Valkey has none.
export const engineLogo = (engine: string) => ({ postgres: 'postgresql', mongo: 'mongodb' } as Record<string, string>)[engine] ?? engine;

// What the database is doing: the node's systemd state, or why we don't know it.
export function dbState(d: Database): State {
  if (d.node_down) return ['offline', 'node offline'];
  if (d.state === 'active') return ['running', 'running'];
  if (d.state === 'activating') return ['activating', 'starting'];
  return [d.state || 'stopped', d.state || 'unknown'];
}

/* ---------- formatting ---------- */
export function size(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(2)} GB`;
}
