// Small pieces that show data: chips, meters, copyable text and commands.
import { useState, type CSSProperties, type ReactNode } from 'react';
import { Icon } from './icons';
import { useToast } from './feedback';

export const Chip = ({ children }: { children: ReactNode }) => <span className="chip">{children}</span>;

export function Meter({ label, pct, text }: { label: string; pct: number; text: string }) {
  const p = Math.max(0, Math.min(100, Math.round(pct || 0)));
  const lvl = p >= 85 ? 'b-bad' : p >= 70 ? 'b-busy' : '';
  return (
    <div className="meter">
      <div className="meter-h"><span>{label}</span><span className="mono od-nowrap">{text}</span></div>
      <div className="bar" role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={p}><i className={lvl} style={{ '--pct': `${p}%` } as CSSProperties} /></div>
    </div>
  );
}

// navigator.clipboard needs a secure context; http://walroos-hub on the tailnet isn't one.
function copyText(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  const ok = document.execCommand('copy');
  ta.remove();
  return ok ? Promise.resolve() : Promise.reject(new Error('copy failed'));
}

export function CopyBtn({ text, label = 'Copy' }: { text: string; label?: string }) {
  const toast = useToast();
  const [done, setDone] = useState(false);
  const onClick = () => copyText(text).then(
    () => { toast('Copied to clipboard'); setDone(true); setTimeout(() => setDone(false), 1200); },
    () => toast('Copy failed: select the text and copy it', 'bad'),
  );
  return (
    <button type="button" className={`btn-icon${done ? ' copied' : ''}`} aria-label={label} title={label} onClick={onClick}>
      <Icon n={done ? 'check' : 'copy'} />
    </button>
  );
}

export const Cmd = ({ text }: { text: string }) => (
  <div className="cmd"><span className="prompt" aria-hidden="true">$</span><code>{text}</code><CopyBtn text={text} label="Copy command" /></div>
);
