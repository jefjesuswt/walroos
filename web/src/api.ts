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
  repo_url: string;
  branch: string;
  dockerfile: string;
  port: number;
  health_path: string;
  domains: string[]; // public hostnames through the node's Cloudflare Tunnel
  created_at: string;
  last_deployment?: Deployment;
  running?: Release;
  node_down?: boolean;
}

export interface NewApp {
  name: string;
  repo_url: string;
  branch?: string;
  dockerfile?: string;
  port: number;
  health_path?: string;
  node?: string;
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
  joinToken: () => req<JoinToken>('POST', '/api/nodes/join-tokens'),

  workspaces: () => req<Workspace[]>('GET', '/api/workspaces'),
  workspace: (name: string) => req<Workspace>('GET', `/api/workspaces/${name}`),
  createWorkspace: (w: NewWorkspace) => req<Workspace>('POST', '/api/workspaces', w),
  detect: (repo_url: string, branch: string, node: string) => req<Detection>('POST', '/api/workspaces/detect', { repo_url, branch, node }),
  startWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/start`),
  stopWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/stop`),
  rebuildWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/rebuild`),
  workspaceEnv: (name: string) => req<EnvVar[]>('GET', `/api/workspaces/${name}/env`),
  setWorkspaceEnv: (name: string, vars: EnvVar[]) => req<EnvVar[]>('PUT', `/api/workspaces/${name}/env`, vars),
  deleteWorkspace: (name: string) => req<void>('DELETE', `/api/workspaces/${name}`),

  apps: () => req<App[]>('GET', '/api/apps'),
  app: (name: string) => req<App>('GET', `/api/apps/${name}`),
  createApp: (a: NewApp) => req<App>('POST', '/api/apps', a),
  deleteApp: (name: string) => req<void>('DELETE', `/api/apps/${name}`),
  deployApp: (name: string, sha = '') => req<Deployment>('POST', `/api/apps/${name}/deploy`, { sha }),
  deployments: (name?: string) => req<Deployment[]>('GET', name ? `/api/apps/${name}/deployments` : '/api/deployments'),
  deployment: (name: string, id: string) => req<Deployment>('GET', `/api/apps/${name}/deployments/${id}`),
  rollback: (name: string, id: string) => req<Deployment>('POST', `/api/apps/${name}/deployments/${id}/rollback`),
  appLogs: (name: string, lines = 300) => req<string>('GET', `/api/apps/${name}/logs?lines=${lines}`),
  appEnv: (name: string) => req<EnvVar[]>('GET', `/api/apps/${name}/env`),
  setAppEnv: (name: string, vars: EnvVar[]) => req<EnvVar[]>('PUT', `/api/apps/${name}/env`, vars),
  setDomains: (name: string, domains: string[]) => req<App>('PUT', `/api/apps/${name}/domains`, domains),
  cloudflare: () => req<Cloudflare>('GET', '/api/settings/cloudflare'),
  setCloudflare: (token: string) => req<Cloudflare>('PUT', '/api/settings/cloudflare', { token }),

  sshKeys: () => req<SSHKey[]>('GET', '/api/ssh-keys'),
  addSSHKey: (name: string, public_key: string) => req<SSHKey>('POST', '/api/ssh-keys', { name, public_key }),
  removeSSHKey: (id: string) => req<void>('DELETE', `/api/ssh-keys/${id}`),
};
