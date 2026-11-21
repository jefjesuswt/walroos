// Form building blocks. Every dialog and settings card is made of these, so labels, hints and
// rows line up the same everywhere: don't lay out a form by hand.
import type { ReactNode } from 'react';
import { Icon } from './icons';

// Field is label + control + hint. optional adds "(optional)" to the label; tone="bad" shows the
// hint as a problem (no node with the role, a value that won't work).
export function Field({ label, hint, optional, tone, children }: {
  label: ReactNode; hint?: ReactNode; optional?: boolean; tone?: 'bad'; children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="label">{label}{optional && <> <span className="muted">(optional)</span></>}</span>
      {children}
      {hint && <span className={tone === 'bad' ? 'help help-bad' : 'help'}>{hint}</span>}
    </label>
  );
}

// FormRow puts two or three fields side by side; on phones they stack. The fields keep their
// labels and controls on the same lines whatever their hints are.
export const FormRow = ({ children }: { children: ReactNode }) => <div className="field-row">{children}</div>;

// Select is a native select that hands back the chosen value.
export function Select({ value, onChange, mono, disabled, label, children }: {
  value: string; onChange: (v: string) => void; mono?: boolean; disabled?: boolean; label?: string; children: ReactNode;
}) {
  return (
    <select className={mono ? 'input mono' : 'input'} value={value} disabled={disabled} aria-label={label} onChange={e => onChange(e.target.value)}>
      {children}
    </select>
  );
}

// Check is a checkbox with its text.
export function Check({ checked, onChange, title, disabled, children }: {
  checked: boolean; onChange: (v: boolean) => void; title?: string; disabled?: boolean; children: ReactNode;
}) {
  return (
    <label className="check" title={title}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={e => onChange(e.target.checked)} />{children}
    </label>
  );
}

// Seg is a segmented button: one of a few choices, the chosen one ticked.
export function Seg<T extends string | boolean>({ label, value, onChange, options }: {
  label: string; value: T; onChange: (v: T) => void; options: [T, ReactNode][];
}) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map(([v, text]) => (
        <button key={String(v)} type="button" aria-pressed={v === value} onClick={() => onChange(v)}>{v === value && <Icon n="check" />}{text}</button>
      ))}
    </div>
  );
}

// Disclosure hides the fields most people leave alone.
export const Disclosure = ({ summary, children }: { summary: ReactNode; children: ReactNode }) => (
  <details className="disclosure"><summary>{summary}</summary><div className="form">{children}</div></details>
);
