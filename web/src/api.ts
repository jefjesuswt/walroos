// Types mirror internal/fleet/models.go and internal/auth/auth.go.

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

async function req<T>(method: string, path: string): Promise<T> {
  const res = await fetch(path, { method, headers: { Accept: 'application/json' } });
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
};
