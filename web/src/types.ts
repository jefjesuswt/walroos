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

export type UserRole = 'owner' | 'deployer' | 'developer';
export const USER_ROLES: UserRole[] = ['owner', 'deployer', 'developer'];

export interface User {
  login: string;
  role: UserRole;
}

export interface Member extends User {
  created_at: string;
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

// A stack a workspace from scratch can start from (GET /api/workspaces/stacks). Versions are image tags, newest first.
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
  project_id: string;
  project: string; // its project's name
  description: string;
  repo_url: string; // '' when image is set; both '' = a draft with no source yet
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
  project?: string; // name; '' = default
  description?: string;
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
  project_id: string;
  project: string;
  description: string;
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

// A project groups apps and databases; they share a network on their node and reach each other by name.
export interface Project {
  id: string;
  name: string;
  description: string;
  created_at: string;
  apps: App[];
  databases: Database[];
}

// A common service ready to run from a published image (GET /api/templates).
export interface Template {
  id: string;
  name: string;
  category: string;
  description: string;
  logo?: string;
  image: string;
  port: number;
  health_path: string;
}
