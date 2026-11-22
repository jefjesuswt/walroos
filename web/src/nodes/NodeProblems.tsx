// What the agents found wrong on their nodes (a kernel updated without a reboot, containers with
// no internet), each with its fix. The nodes page shows them in full; every other page shows a
// short line pointing there, since the node still looks online.
import { useState } from 'react';
import { api, type Node } from '../api';
import { Alert, usePoll } from '../ui';

const problems = (nodes: Node[]) => nodes.filter(n => n.status === 'online').flatMap(n => (n.telemetry.problems ?? []).map(p => ({ node: n.name, ...p })));

export function NodeProblems({ nodes }: { nodes: Node[] }) {
  return <>
    {problems(nodes).map(p => (
      <Alert key={p.node + p.code} title={<><span className="mono">{p.node}</span>: {p.title}</>}>{p.fix}</Alert>
    ))}
  </>;
}

// NodeProblemsBanner polls the nodes on its own, for the pages that don't list them.
export function NodeProblemsBanner() {
  const [nodes, setNodes] = useState<Node[]>([]);
  usePoll(() => { api.nodes().then(setNodes, () => undefined); }, 30000);
  const list = problems(nodes);
  if (!list.length) return null;
  return (
    <Alert title={list.length === 1 ? <><span className="mono">{list[0].node}</span>: {list[0].title}</> : `${list.length} problems on your nodes`}>
      <a href="#/nodes">See the fix on Nodes</a>
    </Alert>
  );
}
