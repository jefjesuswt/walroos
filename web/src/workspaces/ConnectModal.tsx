import type { Workspace } from '../api';
import { Cmd, CopyBtn, Modal } from '../ui';

// ConnectModal: the commands to reach a workspace from a terminal or an editor.
export function ConnectModal({ w, onClose }: { w: Workspace; onClose: () => void }) {
  const alias = `${w.name}.walroos`;
  return (
    <Modal title={`Connect to ${w.name}`} wide onClose={onClose}>
      <div className="form">
        <div className="field">
          <span className="label">1 · SSH config, again when you add a workspace</span>
          {/* The CLI defaults to walroos-hub; the hub you opened this page on may have another name. */}
          <Cmd text={`walroos ssh-config --hub ${window.location.host} --write`} />
          <span className="help">Keeps every workspace in a walroos section of <span className="mono">~/.ssh/config</span>. With <span className="mono">tailscaled</span> in userspace mode it goes through its proxy on <span className="mono">localhost:1055</span> by itself.</span>
        </div>
        <div className="field">
          <span className="label">2 · A shell in the container</span>
          <div className="cmd"><span className="prompt" aria-hidden="true">$</span><code>ssh {alias}</code><CopyBtn text={`ssh ${alias}`} label="Copy SSH command" /></div>
          <span className="help">A stopped workspace starts on connect. <span className="mono">claude</span>, bun and your runtimes live inside.</span>
        </div>
        <div className="field">
          <span className="label">3 · Your editor, opened on the repo</span>
          {w.folder ? <>
            {/* Without a path, Zed and VS Code open $HOME: hand them the repo folder. */}
            <Cmd text={`zed ssh://${alias}${w.folder}`} />
            <Cmd text={`code --remote ssh-remote+${alias} ${w.folder}`} />
          </> : (
            <span className="help">Connect to <span className="mono">{alias}</span> and open <span className="mono">/workspaces/&lt;repo&gt;</span> (the node didn't answer, so the exact folder isn't known).</span>
          )}
          <span className="help">Zed's <em>Open Remote</em> lists every workspace with its repo after this (run it again when you add one):</span>
          <Cmd text={`walroos ssh-config --hub ${window.location.host} --zed --write`} />
          <span className="help">Claude Code: Remote-SSH to <span className="mono">{alias}</span>. Ports: <span className="mono">ssh -L 3000:localhost:3000 {alias}</span>.</span>
        </div>
        <div className="field">
          <span className="label">On the node</span>
          <span className="help mono od-truncate">{w.node_name} · {w.image || 'image pending'}</span>
          <span className="help mono od-truncate" title={w.repo_url}>{w.repo_url || 'Local git repo: add origin and push from inside'}{w.branch ? ` (${w.branch})` : ''}</span>
        </div>
      </div>
    </Modal>
  );
}
