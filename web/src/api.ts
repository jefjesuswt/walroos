// Types mirror internal/fleet/models.go, internal/workspace/models.go, internal/sshkeys and internal/auth/auth.go.

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
  return res.status === 204 ? (undefined as T) : res.json();
}

export const api = {
  me: () => req<User>('GET', '/api/me'),
  nodes: () => req<Node[]>('GET', '/api/nodes'),
  removeNode: (id: string) => req<void>('DELETE', `/api/nodes/${id}`),
  joinToken: () => req<JoinToken>('POST', '/api/nodes/join-tokens'),

  workspaces: () => req<Workspace[]>('GET', '/api/workspaces'),
  workspace: (name: string) => req<Workspace>('GET', `/api/workspaces/${name}`),
  createWorkspace: (w: NewWorkspace) => req<Workspace>('POST', '/api/workspaces', w),
  startWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/start`),
  stopWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/stop`),
  rebuildWorkspace: (name: string) => req<Workspace>('POST', `/api/workspaces/${name}/rebuild`),
  deleteWorkspace: (name: string) => req<void>('DELETE', `/api/workspaces/${name}`),

  sshKeys: () => req<SSHKey[]>('GET', '/api/ssh-keys'),
  addSSHKey: (name: string, public_key: string) => req<SSHKey>('POST', '/api/ssh-keys', { name, public_key }),
  removeSSHKey: (id: string) => req<void>('DELETE', `/api/ssh-keys/${id}`),
};
