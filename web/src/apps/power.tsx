// Stop, start and restart an app's running release: the actions its page header and its card's
// right-click menu offer. Stopping asks first: the app goes offline until it's started again.
import { useState, type ReactNode } from 'react';
import { api, type App } from '../api';
import { type Action, Confirm, useToast } from '../ui';

type Power = 'stop' | 'start' | 'restart';
const DONE: Record<Power, string> = { stop: 'stopped', start: 'started', restart: 'restarted' };

export function usePower(onChange: (a: App) => void): [(a: App) => Action[], ReactNode] {
  const toast = useToast();
  const [stopping, setStopping] = useState<App | null>(null);
  const power = (a: App, action: Power) => api.powerApp(a.name, action).then(
    x => { toast(`${a.name} ${DONE[action]}`); onChange(x); },
    e => toast(e.message, 'bad'),
  );
  const actions = (a: App): Action[] => !a.running || a.node_down ? []
    : a.running.state === 'inactive' ? [{ icon: 'play', label: 'Start', run: () => power(a, 'start') }]
    : [
      { icon: 'refresh', label: 'Restart', run: () => power(a, 'restart') },
      { icon: 'stop', label: 'Stop', run: () => setStopping(a), danger: true },
    ];
  const dialog = stopping && (
    <Confirm title={`Stop ${stopping.name}?`} confirmLabel="Stop" danger onClose={() => setStopping(null)}
      onConfirm={() => power(stopping, 'stop').finally(() => setStopping(null))}>
      <p className="muted">It stops answering{stopping.domains.length ? ` on ${stopping.domains.join(', ')}` : ''}. It stays stopped, even when <span className="mono">{stopping.node_name}</span> restarts, until you start it or deploy it again.</p>
    </Confirm>
  );
  return [actions, dialog];
}
