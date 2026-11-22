// The right-click menu: the same Actions a row's buttons or ⋮ sheet show, opened where the
// pointer is. On a phone (a long press) they open in the ActionSheet instead. Shift+right-click
// keeps the browser's own menu.
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Icon } from './icons';
import { ActionSheet, type Action } from './overlay';

type Open = { x: number; y: number; title: string; actions: Action[] };

// linkActions are what a card that is a link offers first, since the menu replaces the browser's:
// open it here or in a new tab.
export const linkActions = (href: string): Action[] => [
  { icon: 'eye', label: 'Open', run: () => { location.hash = href; } },
  { icon: 'external', label: 'Open in a new tab', run: () => { window.open(href, '_blank', 'noopener'); } },
];

const phone = () => window.matchMedia('(max-width: 640px), (pointer: coarse)').matches;

// useContextMenu returns onMenu(title, actions), the onContextMenu handler for an element, and
// the menu to render once in the page.
export function useContextMenu(): [(title: string, actions: Action[]) => (e: MouseEvent) => void, ReactNode] {
  const [open, setOpen] = useState<Open | null>(null);
  const onMenu = (title: string, actions: Action[]) => (e: MouseEvent) => {
    if (e.shiftKey || actions.length === 0) return;
    e.preventDefault();
    e.stopPropagation(); // a card inside a card: the inner one's menu
    // The context-menu key opens at 0,0: put the menu on the element instead.
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const key = e.clientX === 0 && e.clientY === 0;
    setOpen({ x: key ? r.left + 16 : e.clientX, y: key ? r.top + 16 : e.clientY, title, actions });
  };
  const close = () => setOpen(null);
  const menu = !open ? null
    : phone() ? <ActionSheet title={open.title} actions={open.actions} onClose={close} />
    : <ContextMenu {...open} onClose={close} />;
  return [onMenu, menu];
}

function ContextMenu({ x, y, title, actions, onClose }: Open & { onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ left: x, top: y });
  useLayoutEffect(() => { // keep it on screen: flip left or up near the edges
    const el = ref.current;
    if (!el) return;
    const { width, height } = el.getBoundingClientRect();
    setPos({ left: x + width > innerWidth - 8 ? Math.max(8, x - width) : x, top: y + height > innerHeight - 8 ? Math.max(8, y - height) : y });
  }, [x, y]);
  useEffect(() => {
    const last = document.activeElement as HTMLElement | null;
    items()[0]?.focus();
    const onKey = (e: KeyboardEvent) => {
      const list = items(), i = list.indexOf(document.activeElement as HTMLButtonElement);
      if (e.key === 'Escape' || e.key === 'Tab') { e.preventDefault(); onClose(); }
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        list[(i + (e.key === 'ArrowDown' ? 1 : list.length - 1)) % list.length]?.focus();
      }
    };
    const away = () => onClose();
    document.addEventListener('keydown', onKey);
    addEventListener('resize', away);
    addEventListener('scroll', away, true);
    return () => {
      document.removeEventListener('keydown', onKey);
      removeEventListener('resize', away);
      removeEventListener('scroll', away, true);
      last?.focus();
    };
  }, []);
  const items = () => [...(ref.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];

  return createPortal(<>
    <div className="ctxmenu-scrim" onMouseDown={onClose} onContextMenu={e => { e.preventDefault(); onClose(); }} />
    <div ref={ref} className="ctxmenu" role="menu" aria-label={title} style={{ '--x': `${pos.left}px`, '--y': `${pos.top}px` } as CSSProperties}>
      <div className="ctxmenu-title mono">{title}</div>
      {actions.map(a => (
        <button key={a.label} role="menuitem" className={a.danger ? 'danger' : ''} disabled={a.off} title={a.title}
          onClick={() => { onClose(); a.run(); }}><Icon n={a.icon} />{a.label}</button>
      ))}
    </div>
  </>, document.body);
}
