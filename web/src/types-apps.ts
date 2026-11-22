// The apps' types: they mirror internal/app/models.go and internal/agent/apps.go. types.ts re-exports them.

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

// How an app deploys by itself: the button only, a push webhook, or CI after the tests pass.
export type Trigger = 'manual' | 'on_push' | 'on_ci';

// Where the forge reaches the hub's hooks (Tailscale Funnel), or why it can't.
export interface Hooks {
  url: string;
  error?: string;
}

// A service of the project (or "shared") and the variables ${{service.KEY}} can name.
export interface Ref {
  service: string;
  keys: string[];
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
  static?: StaticBuild; // a repo with no Dockerfile, built with Node or Bun and served on port 8080
  health?: Health; // how a release proves it's up; '' is http
  command?: string[]; // replaces the image's CMD
  context?: string; // the repo folder it builds from
  compose?: string; // the compose service it comes from (its name on the project's network)
  port: number;
  health_path: string;
  domains: string[]; // public hostnames through the node's Cloudflare Tunnel
  volumes: Volume[]; // apply on the next deploy
  trigger: Trigger;
  deploy_token: boolean; // CI has a deploy token (shown once, when made)
  created_at: string;
  last_deployment?: Deployment;
  running?: Release;
  node_down?: boolean;
}

// How a new release proves it's up: GET a path (http), the port accepts connections (tcp), or it
// keeps running (a worker with no port).
export type Health = '' | 'http' | 'tcp' | 'running';

// How a static site builds. Every field is optional: walroos finds them from the repo.
export interface StaticBuild {
  runtime?: '' | 'node' | 'bun'; // '' picks bun with a bun lockfile, else node
  install?: string;
  build?: string;
  publish?: string; // the folder to serve, relative to the repo
  spa?: boolean; // paths with no file get index.html
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
