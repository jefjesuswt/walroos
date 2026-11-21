import { useMemo, useState, type CSSProperties } from 'react';
import { addCustomTheme, allThemes, applyTheme, contrastWarnings, parseTheme, pick, removeCustomTheme, savedPref, type Mode, type Theme } from '../theme';
import { Alert, Field, Icon, Modal, Section, Seg, useToast } from '../ui';

const MODES: [Mode, string][] = [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']];

export function AppearanceCard() {
  const toast = useToast();
  const [pref, setPref] = useState(savedPref);
  const [themes, setThemes] = useState(allThemes);
  const [importing, setImporting] = useState(false);

  const choose = (id: string, mode: Mode = pref.mode) => { applyTheme(id, mode); setPref({ id, mode }); };
  const remove = (t: Theme) => {
    removeCustomTheme(t.id);
    setThemes(allThemes());
    if (pref.id === t.id) choose('walroos');
    toast(`Theme "${t.name}" removed`);
  };

  return (
    <Section id="look-h" title="Appearance" sub="Themes fill Material 3 color roles, so any palette works. Saved in this browser."
      actions={<button className="btn" onClick={() => setImporting(true)}><Icon n="upload" />Import theme</button>}>
        <Seg label="Light or dark" value={pref.mode} onChange={m => choose(pref.id, m)} options={MODES} />
        <div className="swatches">
          {themes.map(t => {
            const { scheme } = pick(t, pref.mode);
            const modes = t.schemes.light && t.schemes.dark ? 'Light and dark' : t.schemes.dark ? 'Dark only' : 'Light only';
            return (
              <div key={t.id} className="swatch-wrap">
                <button className="swatch" aria-pressed={pref.id === t.id} onClick={() => choose(t.id)}
                  style={{ '--sw-bg': scheme.surfaceContainer ?? scheme.surface, '--sw-tx': scheme.onSurface } as CSSProperties}>
                  <span className="swatch-chips" aria-hidden="true">
                    {[scheme.primary, scheme.secondaryContainer ?? scheme.primaryContainer, scheme.success, scheme.error].filter(Boolean)
                      .map((c, i) => <i key={i} style={{ '--c': c } as CSSProperties} />)}
                  </span>
                  <span className="swatch-name">{pref.id === t.id && <Icon n="check" />}{t.name}</span>
                  <span className="swatch-sub">{t.custom ? 'Imported · ' : ''}{modes}</span>
                </button>
                {t.custom && <button className="btn-icon" aria-label={`Remove ${t.name}`} title="Remove theme" onClick={() => remove(t)}><Icon n="trash" /></button>}
              </div>
            );
          })}
        </div>
      {importing && <ImportThemeModal onClose={() => setImporting(false)}
        onImported={t => { addCustomTheme(t); setThemes(allThemes()); choose(t.id); setImporting(false); toast(`Theme "${t.name}" added`); }} />}
    </Section>
  );
}

function ImportThemeModal({ onClose, onImported }: { onClose: () => void; onImported: (t: Theme) => void }) {
  const [name, setName] = useState('');
  const [json, setJson] = useState('');
  const parsed = useMemo(() => {
    if (!json.trim()) return null;
    try { const t = parseTheme(json, name); return { t, warnings: contrastWarnings(t) }; }
    catch (e) { return { error: (e as Error).message }; }
  }, [json, name]);

  const readFile = (f?: File) => f?.text().then(text => { setJson(text); if (!name) setName(f.name.replace(/\.json$/i, '')); });

  return (
    <Modal title="Import theme" wide onClose={onClose}
      sub="Paste or pick the JSON that Material Theme Builder exports. Roles it leaves out are derived from the ones it has."
      foot={<>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" disabled={!parsed || !('t' in parsed)} onClick={() => parsed && 't' in parsed && parsed.t && onImported(parsed.t)}>Import</button>
      </>}>
      <div className="form">
        <Field label="Name"><input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="Catppuccin Frappé" autoFocus /></Field>
        <Field label="Theme JSON" hint={<>Needs <span className="mono">schemes.light</span> and/or <span className="mono">schemes.dark</span> with at least primary, onPrimary, surface and onSurface. <a className="link" href="https://material-foundation.github.io/material-theme-builder/" target="_blank" rel="noreferrer">Material Theme Builder</a> exports this.</>}>
          <textarea className="input mono" rows={8} value={json} onChange={e => setJson(e.target.value)} placeholder='{"schemes": {"dark": {"primary": "#CBA6F7", "onPrimary": "#11111B", "surface": "#1E1E2E", "onSurface": "#CDD6F4"}}}' />
        </Field>
        <div><label className="btn"><Icon n="upload" />Pick a .json file<input type="file" accept=".json,application/json" hidden onChange={e => readFile(e.target.files?.[0])} /></label></div>
        {parsed && 'error' in parsed && <Alert title="Can't use this JSON">{parsed.error}</Alert>}
        {parsed && 'warnings' in parsed && parsed.warnings && parsed.warnings.length > 0 && (
          <Alert tone="busy" title="Some text may be hard to read">Below WCAG AA (4.5:1): {parsed.warnings.join('; ')}. You can import it anyway.</Alert>
        )}
      </div>
    </Modal>
  );
}
