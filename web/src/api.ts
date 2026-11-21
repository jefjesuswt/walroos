// Types mirror internal/fleet/models.go, internal/workspace/models.go, internal/app/models.go, internal/sshkeys and internal/auth/auth.go.

export type Role = 'dev' | 'build' | 'prod';
export const ROLES: Role[] = ['dev', 'build', 'prod'];

export interface Telemetry {
  hostname: string;
  os: string;
  arch: string;
  cpu_percent: number;
  mem_used_mb: number;
  mem_total_mb: number;
  disk_used_gb: number;
  disk_total_gb: number;
  podman: string;
  walroos: string;
  containers: number;
}

export interface Node {
  id: string;
  name: string;
  roles: Role[];
  address: string;
  status: 'pending' | 'online' | 'offline';
  telemetry: Telemetry;
  created_at: string;
  last_seen: string | null;
}

export interface User {
  login: string;
  role: 'owner' | 'member';
}

export type WorkspaceState = 'creating' | 'running' | 'stopped' | 'failed' | 'deleting';

export interface Workspace {
  id: string;
  name: string;
  owner: string;
  node_id: string;
  node_name: string;
  repo_url: string;
  branch: string;
  devcontainer_path: string;
  image?: string;
  stack?: string; // what detect found at create time (android, go…); absent = unknown
  folder?: string; // the repo inside the container (/workspaces/<repo>); absent while the node is unreachable
  state: WorkspaceState;
  error?: string;
  created_at: string;
}

export interface NewWorkspace {
  name: string;
  repo_url: string;
  branch?: string;
  devcontainer_path?: string;
  node?: string;
  devcontainer?: Record<string, unknown>; // inline devcontainer.json; wins over the repo's
  stack?: string; // Detection.stack, stored for display
}

// A stack a new project can start from (GET /api/projects/stacks). Versions are image tags, newest first.
export interface ProjectStack {
  id: string;
  label: string;
  versions: string[];
  version_of?: string; // what the versions are of, when not the stack itself (Bun runs on Node)
}

export interface NewProject {
  name: string;
  stack: string;
  version: string;
  github: boolean; // create the repo on GitHub; false = a local git init on the node
  private: boolean;
  node?: string;
}

export interface GitHub {
  configured: boolean;
  login?: string;
}

// What POST /api/workspaces/detect proposes for a repo.
export interface DetectedTool {
  id: string;
  label: string;
  version?: string;
  source: 'image' | 'postCreate';
  install?: string; // joined with && into postCreateCommand
}

export interface Detection {
  stack: string; // android, dotnet, bun, node, go, rust, python, java, devcontainer, none
  branch: string;
  image?: string;
  remote_user?: string;
  tools: DetectedTool[];
  devcontainer?: Record<string, unknown>; // absent: the repo has its own, or nothing matched (base image)
  notes?: string[];
}

export interface EnvVar {
  key: string;
  value: string;
  secret: boolean;
}

export interface SSHKey {
  id: string;
  name: string;
  public_key: string;
  fingerprint: string;
  created_at: string;
}

export interface JoinToken {
  token: string;
  expires_at: string;
  command: string;
}

export type DeployState = 'queued' | 'running' | 'succeeded' | 'failed';

export interface Deployment {
  id: string;
  app_id: string;
  app_name?: string; // only in the all-apps list
  sha: string;
  image: string;
  state: DeployState;
  error?: string;
  log?: string; // only when getting one deployment
  created_at: string;
  finished_at?: string;
}

// The release the node runs, as its agent reports it (internal/agent/apps.go App).
export interface Release {
  name: string;
  container: string; // <app>-<deployment id>
  sha: string;
  image: string;
  state: string; // systemd ActiveState: active, activating, failed...
}

export interface Cloudflare {
  configured: boolean;
  zones: string[];
}

export interface App {
  id: string;
  name: string;
  node_id: string;
  node_name: string;
  repo_url: string; // '' when image is set
  image: string; // a published image, pulled on each deploy, instead of a repo
  branch: string;
  dockerfile: string;
  port: number;
  health_path: string;
  domains: string[]; // public hostnames through the node's Cloudflare Tunnel
  volumes: Volume[]; // apply on the next deploy
  created_at: string;
  last_deployment?: Deployment;
  running?: Release;
  node_down?: boolean;
}

// A named podman volume (name) or a directory under the node's ~/walroos-data (host_path).
export interface Volume {
  name?: string;
  host_path?: string;
  target: string;
  ro?: boolean;
}

export interface NewApp {
  name: string;
  repo_url?: string;
  image?: string; // instead of repo_url
  branch?: string;
  dockerfile?: string;
  port: number;
  health_path?: string;
  node?: string;
}

// A database engine (GET /api/databases/engines). Versions are image tags, newest first.
export interface Engine {
  id: string;
  label: string;
  versions: string[];
  port: number;
  scheme: string;
}

export interface Database {
  id: string;
  name: string;
  node_id: string;
  node_name: string;
  engine: string;
  version: string;
  user: string;
  db: string;
  host: string; // container name on the node's walroos network
  port: number;
  url?: string; // with the password; only from GET /api/databases/{name}
  backup_every_hours: number; // 0 = off
  backup_keep: number;
  last_backup_at?: string;
  last_backup_error?: string;
  created_at: string;
  state?: string; // systemd ActiveState on the node; 'missing' if the node doesn't have it
  node_down?: boolean;
}

export interface Backup {
  file: string;
  size: number;
  created_at: string;
}

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
  projectStacks: () => req<ProjectStack[]>('GET', '/api/projects/stacks'),
  createProject: (p: NewProject) => req<Workspace>('POST', '/api/projects', p),
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
  deployments: (name?: string) => req<Deployment[]>('GET', name ? `/api/apps/${name}/deployments` : '/api/deployments'),
  deployment: (name: string, id: string) => req<Deployment>('GET', `/api/apps/${name}/deployments/${id}`),
  rollback: (name: string, id: string) => req<Deployment>('POST', `/api/apps/${name}/deployments/${id}/rollback`),
  appLogs: (name: string, lines = 300) => req<string>('GET', `/api/apps/${name}/logs?lines=${lines}`),
  appEnv: (name: string) => req<EnvVar[]>('GET', `/api/apps/${name}/env`),
  setAppEnv: (name: string, vars: EnvVar[]) => req<EnvVar[]>('PUT', `/api/apps/${name}/env`, vars),
  setDomains: (name: string, domains: string[]) => req<App>('PUT', `/api/apps/${name}/domains`, domains),
  setVolumes: (name: string, volumes: Volume[]) => req<App>('PUT', `/api/apps/${name}/volumes`, volumes),
  cloudflare: () => req<Cloudflare>('GET', '/api/settings/cloudflare'),
  setCloudflare: (token: string) => req<Cloudflare>('PUT', '/api/settings/cloudflare', { token }),
  github: () => req<GitHub>('GET', '/api/settings/github'),
  setGitHub: (token: string) => req<GitHub>('PUT', '/api/settings/github', { token }),
  removeGitHub: () => req<void>('DELETE', '/api/settings/github'),

  engines: () => req<Engine[]>('GET', '/api/databases/engines'),
  databases: () => req<Database[]>('GET', '/api/databases'),
  database: (name: string) => req<Database>('GET', `/api/databases/${name}`),
  createDatabase: (d: { name: string; engine: string; version: string; node?: string }) => req<Database>('POST', '/api/databases', d),
  deleteDatabase: (name: string, purge = false) => req<void>('DELETE', `/api/databases/${name}${purge ? '?purge=1' : ''}`),
  setSchedule: (name: string, backup_every_hours: number, backup_keep: number) =>
    req<Database>('PUT', `/api/databases/${name}/schedule`, { backup_every_hours, backup_keep }),
  linkDatabase: (name: string, app: string, key: string) => req<EnvVar[]>('POST', `/api/databases/${name}/link`, { app, key }),
  backups: (name: string) => req<Backup[]>('GET', `/api/databases/${name}/backups`),
  backupNow: (name: string) => req<Backup>('POST', `/api/databases/${name}/backups`),
  restore: (name: string, file: string) => req<void>('POST', `/api/databases/${name}/restore`, { file }),
  backupURL: (name: string, file: string) => `/api/databases/${name}/backups/${encodeURIComponent(file)}`,

  sshKeys: () => req<SSHKey[]>('GET', '/api/ssh-keys'),
  addSSHKey: (name: string, public_key: string) => req<SSHKey>('POST', '/api/ssh-keys', { name, public_key }),
  removeSSHKey: (id: string) => req<void>('DELETE', `/api/ssh-keys/${id}`),
};
