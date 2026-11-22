import { describe, expect, test } from 'bun:test';
import type { App, Database, Deployment, User } from '../src/api';
import { allowed } from '../src/ui/auth';
import { ago } from '../src/ui/hooks';
import { appState, dbLabel, dbState, duration, engineLogo, size, tone } from '../src/status';

const app = (over: Partial<App>): App => ({
  id: 'a', name: 'shop', node_id: 'n', node_name: 'prod-1', project: 'default', project_id: 'p', description: '',
  repo_url: 'https://github.com/me/shop', image: '', branch: '', dockerfile: '', port: 3000, health_path: '/',
  domains: [], volumes: [], created_at: '2026-01-01T00:00:00Z', node_down: false, ...over,
} as App);
const dep = (state: Deployment['state']) => ({ id: 'd', state, created_at: '2026-01-01T00:00:00Z' } as Deployment);

describe('appState', () => {
  test.each([
    ['node down wins', app({ node_down: true, running: { state: 'active' } as App['running'] }), ['offline', 'node offline']],
    ['a draft has no source', app({ repo_url: '' }), ['stopped', 'no source yet']],
    ['queued', app({ last_deployment: dep('queued') }), ['deploying', 'queued']],
    ['deploying', app({ last_deployment: dep('running') }), ['deploying', 'deploying']],
    ['the live release beats a failed deploy', app({ last_deployment: dep('failed'), running: { state: 'active' } as App['running'] }), ['running', 'running']],
    ['failed', app({ last_deployment: dep('failed') }), ['failed', 'deploy failed']],
    ['never deployed', app({}), ['stopped', 'not deployed']],
    ['stopped by its owner', app({ running: { state: 'inactive' } as App['running'] }), ['stopped', 'stopped']],
  ])('%s', (_, a, want) => expect(appState(a as App)).toEqual(want as [string, string]));
});

describe('dbState and labels', () => {
  const db = (over: Partial<Database>) => ({ engine: 'postgres', version: '17', state: 'active', node_down: false, ...over } as Database);
  test('states', () => {
    expect(dbState(db({}))).toEqual(['running', 'running']);
    expect(dbState(db({ state: 'activating' }))).toEqual(['activating', 'starting']);
    expect(dbState(db({ node_down: true }))).toEqual(['offline', 'node offline']);
    expect(dbState(db({ state: '' }))).toEqual(['stopped', 'unknown']);
  });
  test('labels and logos', () => {
    expect(dbLabel(db({ engine: 'mongo', version: '8' }))).toBe('MongoDB 8');
    expect(dbLabel(db({ engine: 'other', version: '1' }))).toBe('other 1');
    expect(engineLogo('postgres')).toBe('postgresql');
    expect(engineLogo('valkey')).toBe('valkey');
  });
  test('badge tones', () => {
    expect(tone('activating')).toBe('busy');
    expect(tone('missing')).toBe('bad');
    expect(tone('whatever')).toBe('idle');
  });
});

test('duration and size', () => {
  const d = { created_at: '2026-01-01T00:00:00Z', finished_at: '2026-01-01T00:01:05Z' } as Deployment;
  expect(duration(d)).toBe('1m 05s');
  expect(duration({ ...d, finished_at: '2026-01-01T00:00:42Z' })).toBe('42s');
  expect(size(512)).toBe('512 B');
  expect(size(1536)).toBe('1.5 KB');
  expect(size(3 * 1024 ** 3)).toBe('3.00 GB');
});

test('ago', () => {
  const at = (s: number) => new Date(Date.now() - s * 1000).toISOString();
  expect(ago(null)).toBe('never');
  expect(ago(at(3))).toBe('just now');
  expect(ago(at(30))).toBe('30s ago');
  expect(ago(at(600))).toBe('10 min ago');
  expect(ago(at(7200))).toBe('2h ago');
  expect(ago(at(3 * 86400))).toBe('3d ago');
});

test('allowed mirrors internal/auth/permissions.go', () => {
  const u = (role: string) => ({ login: 'x@github', role } as User);
  expect(allowed(u('owner'), 'nodes')).toBe(true);
  expect(allowed(u('deployer'), 'apps')).toBe(true);
  expect(allowed(u('deployer'), 'settings')).toBe(false);
  expect(allowed(u('developer'), 'apps')).toBe(false);
  expect(allowed(null, 'apps')).toBe(false);
});
