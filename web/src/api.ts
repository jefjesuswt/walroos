// The hub's HTTP API. Its types are in types.ts; they're re-exported here, so pages import both from './api'.
import { type Role, type Node, type UserRole, type User, type Member, type Workspace, type NewWorkspace, type ProjectStack, type NewProject, type GitHub, type Detection, type EnvVar, type SSHKey, type JoinToken, type Deployment, type Cloudflare, type App, type Volume, type NewApp, type Engine, type Database, type Backup, type Project, type Template, type Trigger, type Hooks, type Ref, type StaticBuild, type Health, type ComposeSource, type ComposeDiff } from './types';

export * from './types';

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error || `${method} ${path} → ${res.status}`);
  }
  if (res.status === 204) return undefined as T;
  return (res.headers.get('Content-Type') ?? '').startsWith('text/plain') ? res.text() as T : res.json();
}

export const api = {
  me: () => req<User>('GET', '/api/me'),
  nodes: () => req<Node[]>('GET', '/api/nodes'),
  removeNode: (id: string) => req<void>('DELETE', `/api/nodes/${id}`),
  setNodeRoles: (id: string, roles: Role[]) => req<Node>('PUT', `/api/nodes/${id}/roles`, { roles }),
  joinToken: () => req<JoinToken>('POST', '/api/nodes/join-tokens'),

  workspaces: () => req<Workspace[]>('GET', '/api/workspaces'),
  workspace: (name: string) => req<Workspace>('GET', `/api/workspaces/${name}`),
  createWorkspace: (w: NewWorkspace) => req<Workspace>('POST', '/api/workspaces', w),
  detect: (repo_url: string, branch: string, node: string) => req<Detection>('POST', '/api/workspaces/detect', { repo_url, branch, node }),
  projectStacks: () => req<ProjectStack[]>('GET', '/api/workspaces/stacks'),
  createFromScratch: (p: NewProject) => req<Workspace>('POST', '/api/workspaces/from-scratch', p),
  startWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/start`),
  stopWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/stop`),
  rebuildWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/rebuild`),
  workspaceEnv: (name: string) => req<EnvVar[]>('GET', `/api/workspaces/${name}/env`),
  setWorkspaceEnv: (name: string, vars: EnvVar[]) => req<EnvVar[]>('PUT', `/api/workspaces/${name}/env`, vars),
  deleteWorkspace: (name: string) => req<void>('DELETE', `/api/workspaces/${name}`),

  apps: () => req<App[]>('GET', '/api/apps'),
  app: (name: string) => req<App>('GET', `/api/apps/${name}`),
  createApp: (a: NewApp) => req<App>('POST', '/api/apps', a),
  deleteApp: (name: string, purge = false) => req<void>('DELETE', `/api/apps/${name}${purge ? '?purge=1' : ''}`),
  deployApp: (name: string, sha = '') => req<Deployment>('POST', `/api/apps/${name}/deploy`, { sha }),
  powerApp: (name: string, action: 'stop' | 'start' | 'restart') => req<App>('POST', `/api/apps/${name}/power`, { action }),
  deployments: (name?: string) => req<Deployment[]>('GET', name ? `/api/apps/${name}/deployments` : '/api/deployments'),
  deployment: (name: string, id: string) => req<Deployment>('GET', `/api/apps/${name}/deployments/${id}`),
  rollback: (name: string, id: string) => req<Deployment>('POST', `/api/apps/${name}/deployments/${id}/rollback`),
  appLogs: (name: string, lines = 300) => req<string>('GET', `/api/apps/${name}/logs?lines=${lines}`),
  appEnv: (name: string) => req<EnvVar[]>('GET', `/api/apps/${name}/env`),
  setAppEnv: (name: string, vars: EnvVar[]) => req<EnvVar[]>('PUT', `/api/apps/${name}/env`, vars),
  setDomains: (name: string, domains: string[]) => req<App>('PUT', `/api/apps/${name}/domains`, domains),
  templates: () => req<Template[]>('GET', '/api/templates'),
  createFromTemplate: (t: { template: string; name: string; project?: string; node?: string }) => req<App>('POST', '/api/apps/from-template', t),
  setAppSource: (name: string, src: { repo_url?: string; image?: string; branch?: string; dockerfile?: string; static?: StaticBuild; port: number; health_path?: string; health?: Health; command?: string[]; context?: string }) =>
    req<App>('PUT', `/api/apps/${name}/source`, src),
  setTrigger: (name: string, trigger: Trigger) => req<App>('PUT', `/api/apps/${name}/trigger`, { trigger }),
  webhookSecret: (name: string) => req<{ secret: string }>('GET', `/api/apps/${name}/webhook-secret`),
  rotateWebhookSecret: (name: string) => req<{ secret: string }>('POST', `/api/apps/${name}/webhook-secret`),
  newDeployToken: (name: string) => req<{ token: string }>('POST', `/api/apps/${name}/deploy-token`),
  revokeDeployToken: (name: string) => req<void>('DELETE', `/api/apps/${name}/deploy-token`),
  hooks: () => req<Hooks>('GET', '/api/hooks'),
  setVolumes: (name: string, volumes: Volume[]) => req<App>('PUT', `/api/apps/${name}/volumes`, volumes),
  cloudflare: () => req<Cloudflare>('GET', '/api/settings/cloudflare'),
  setCloudflare: (token: string) => req<Cloudflare>('PUT', '/api/settings/cloudflare', { token }),
  github: () => req<GitHub>('GET', '/api/settings/github'),
  setGitHub: (token: string) => req<GitHub>('PUT', '/api/settings/github', { token }),
  removeGitHub: () => req<void>('DELETE', '/api/settings/github'),

  projects: () => req<Project[]>('GET', '/api/projects'),
  project: (name: string) => req<Project>('GET', `/api/projects/${name}`),
  createProjectGroup: (name: string, description: string) => req<Project>('POST', '/api/projects', { name, description }),
  deleteProject: (name: string) => req<void>('DELETE', `/api/projects/${name}`),
  projectEnv: (name: string) => req<EnvVar[]>('GET', `/api/projects/${name}/env`),
  setProjectEnv: (name: string, vars: EnvVar[]) => req<void>('PUT', `/api/projects/${name}/env`, vars),
  projectRefs: (name: string) => req<Ref[]>('GET', `/api/projects/${name}/refs`),
  composeSource: (name: string) => req<ComposeSource>('GET', `/api/projects/${name}/compose`),
  planCompose: (name: string, src: ComposeSource) => req<ComposeDiff>('POST', `/api/projects/${name}/compose/plan`, src),
  syncCompose: (name: string, src: ComposeSource) => req<ComposeDiff>('PUT', `/api/projects/${name}/compose`, src),
  engines: () => req<Engine[]>('GET', '/api/databases/engines'),
  databases: () => req<Database[]>('GET', '/api/databases'),
  database: (name: string) => req<Database>('GET', `/api/databases/${name}`),
  createDatabase: (d: { name: string; engine: string; version: string; node?: string; project?: string; description?: string; db?: string; user?: string; password?: string }) => req<Database>('POST', '/api/databases', d),
  deleteDatabase: (name: string, purge = false) => req<void>('DELETE', `/api/databases/${name}${purge ? '?purge=1' : ''}`),
  setSchedule: (name: string, backup_every_hours: number, backup_keep: number) =>
    req<Database>('PUT', `/api/databases/${name}/schedule`, { backup_every_hours, backup_keep }),
  linkDatabase: (name: string, app: string, key: string) => req<EnvVar[]>('POST', `/api/databases/${name}/link`, { app, key }),
  backups: (name: string) => req<Backup[]>('GET', `/api/databases/${name}/backups`),
  backupNow: (name: string) => req<Backup>('POST', `/api/databases/${name}/backups`),
  restore: (name: string, file: string) => req<void>('POST', `/api/databases/${name}/restore`, { file }),
  backupURL: (name: string, file: string) => `/api/databases/${name}/backups/${encodeURIComponent(file)}`,

  users: () => req<Member[]>('GET', '/api/users'),
  addUser: (login: string, role: UserRole) => req<Member>('POST', '/api/users', { login, role }),
  setUserRole: (login: string, role: UserRole) => req<Member>('PUT', `/api/users/${encodeURIComponent(login)}`, { role }),
  removeUser: (login: string) => req<void>('DELETE', `/api/users/${encodeURIComponent(login)}`),

  sshKeys: () => req<SSHKey[]>('GET', '/api/ssh-keys'),
  addSSHKey: (name: string, public_key: string) => req<SSHKey>('POST', '/api/ssh-keys', { name, public_key }),
  removeSSHKey: (id: string) => req<void>('DELETE', `/api/ssh-keys/${id}`),
};
