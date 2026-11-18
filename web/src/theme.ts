// Themes are Material 3 color roles. Components use only roles (--md-*), never a hex, so any
// palette that fills the roles works: the walroos default, Catppuccin, Dracula, or a JSON
// exported from Material Theme Builder. The same JSON maps 1:1 to a Compose ColorScheme.

export type Mode = 'system' | 'light' | 'dark';
export type Scheme = Record<string, string>; // role (camelCase, as Material Theme Builder exports) → #rrggbb
export type Theme = { id: string; name: string; schemes: { light?: Scheme; dark?: Scheme }; custom?: boolean };

// The roles the UI uses. success/warning aren't M3 roles; Theme Builder exports them as custom colors.
export const ROLES = [
  'primary', 'onPrimary', 'primaryContainer', 'onPrimaryContainer',
  'secondaryContainer', 'onSecondaryContainer',
  'surface', 'onSurface', 'onSurfaceVariant',
  'surfaceContainerLowest', 'surfaceContainerLow', 'surfaceContainer', 'surfaceContainerHigh', 'surfaceContainerHighest',
  'outline', 'outlineVariant',
  'error', 'onError', 'errorContainer', 'onErrorContainer',
  'success', 'successContainer', 'onSuccessContainer',
  'warning', 'warningContainer', 'onWarningContainer',
  'inverseSurface', 'inverseOnSurface', 'scrim',
] as const;

const REQUIRED = ['primary', 'onPrimary', 'surface', 'onSurface'];

const S = (keys: string, hex: string): Scheme => {
  const k = keys.split(' '), v = hex.split(' ');
  return Object.fromEntries(k.map((key, i) => [key, v[i]]));
};
const KEYS = ROLES.join(' ');

export const PRESETS: Theme[] = [
  {
    id: 'walroos', name: 'Walroos', schemes: {
      dark: S(KEYS, '#EE5A96 #1A0510 #7A1F4A #FFD9E4 #3B2633 #FFD9E6 #0B0F14 #E6EDF3 #8B98A5 #080B0F #111820 #151D26 #1B2530 #222D39 #4A5866 #26313D #FF7B72 #3D0A07 #5C1A16 #FFDAD6 #3FB950 #12361E #A6F0B4 #D29922 #3D2E0A #FBE2A6 #E6EDF3 #111820 #000000'),
      light: S(KEYS, '#C21F65 #FFFFFF #FFD9E4 #3E0020 #F6DDE6 #2B1520 #F8F9FB #0D1117 #57606A #FFFFFF #F2F4F7 #ECEFF3 #E6EAEE #DFE4E9 #8C959F #D0D7DE #CF222E #FFFFFF #FFEBE9 #82071E #1A7F37 #DAFBE1 #044F1E #9A6700 #FFF8C5 #4D2D00 #24292F #F6F8FA #000000'),
    },
  },
  {
    id: 'catppuccin', name: 'Catppuccin', schemes: { // Latte / Mocha
      dark: S(KEYS, '#CBA6F7 #11111B #45475A #CDD6F4 #313244 #CDD6F4 #1E1E2E #CDD6F4 #A6ADC8 #181825 #232334 #29293B #313244 #45475A #6C7086 #45475A #F38BA8 #11111B #4A2A37 #F38BA8 #A6E3A1 #2D3B33 #A6E3A1 #F9E2AF #3F3A2E #F9E2AF #CDD6F4 #1E1E2E #000000'),
      light: S(KEYS, '#8839EF #FFFFFF #E5D4FC #4A1A8C #DCE0E8 #4C4F69 #EFF1F5 #4C4F69 #5C5F77 #F7F8FA #E9ECF2 #E6E9EF #DCE0E8 #CCD0DA #9CA0B0 #CCD0DA #D20F39 #FFFFFF #FBD5DC #8A0A26 #40A02B #D8F0D2 #1F5A14 #A86A0D #FBE8CC #6B3F00 #4C4F69 #EFF1F5 #000000'),
    },
  },
  {
    id: 'dracula', name: 'Dracula', schemes: {
      dark: S(KEYS, '#BD93F9 #191A21 #4F3F78 #F8F8F2 #44475A #F8F8F2 #21222C #F8F8F2 #ADB4CF #191A21 #282A36 #2C2E3B #343746 #44475A #6272A4 #44475A #FF5555 #191A21 #4D2A2F #FF9580 #50FA7B #26402F #50FA7B #FFB86C #4A3A2A #FFB86C #F8F8F2 #282A36 #000000'),
    },
  },
  {
    id: 'aura', name: 'Aura', schemes: {
      dark: S(KEYS, '#A277FF #15141B #3D375E #D5C4FF #29263C #EDECEE #15141B #EDECEE #ADACAE #110F18 #1C1B22 #211F2A #29263C #3D375E #6D6D6D #2D2B38 #FF6767 #15141B #4A2228 #FFB3B3 #61FFCA #1D3D36 #61FFCA #FFCA85 #4A3B25 #FFCA85 #EDECEE #15141B #000000'),
    },
  },
];

// Roles an imported theme may leave out, derived from the ones it has.
function fallback(role: string, dark: boolean): string {
  const v = (r: string) => `var(--md-${kebab(r)})`;
  const mix = (a: string, pct: number, b: string) => `color-mix(in srgb, ${a} ${pct}%, ${b})`;
  const on = v('onSurface'), surf = v('surface');
  const base = (dark ? PRESETS[0].schemes.dark : PRESETS[0].schemes.light)!;
  switch (role) {
    case 'primaryContainer': return mix(v('primary'), 24, surf);
    case 'secondaryContainer': return mix(v('primary'), 14, surf);
    case 'onPrimaryContainer': case 'onSecondaryContainer': return on;
    case 'onSurfaceVariant': return mix(on, 70, surf);
    case 'surfaceContainerLowest': return dark ? mix(surf, 80, '#000') : '#FFFFFF';
    case 'surfaceContainerLow': return mix(on, dark ? 4 : 3, surf);
    case 'surfaceContainer': return mix(on, dark ? 6 : 5, surf);
    case 'surfaceContainerHigh': return mix(on, dark ? 10 : 7, surf);
    case 'surfaceContainerHighest': return mix(on, dark ? 14 : 10, surf);
    case 'outline': return mix(on, 40, surf);
    case 'outlineVariant': return mix(on, 15, surf);
    case 'inverseSurface': return on;
    case 'inverseOnSurface': return surf;
    default: return base[role]; // error, success, warning and their pairs, scrim
  }
}

const kebab = (s: string) => s.replace(/[A-Z]/g, c => '-' + c.toLowerCase());

/** The scheme a theme shows in mode: the one asked for, else the only one it has. */
export function pick(t: Theme, mode: Mode): { scheme: Scheme; dark: boolean } {
  const wantDark = mode === 'dark' || (mode === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
  const dark = wantDark ? !!t.schemes.dark || !t.schemes.light : !t.schemes.light;
  return { scheme: (dark ? t.schemes.dark : t.schemes.light)!, dark };
}

/** CSS declarations for a theme in a mode: every role, filled in where the theme has none. */
export function themeCSS(t: Theme, mode: Mode): string {
  const { scheme, dark } = pick(t, mode);
  return ROLES.map(r => `--md-${kebab(r)}:${scheme[r] || fallback(r, dark)}`).join(';') + `;color-scheme:${dark ? 'dark' : 'light'}`;
}

/* ---------- contrast ---------- */

const PAIRS: [string, string][] = [
  ['surface', 'onSurface'], ['surfaceContainer', 'onSurfaceVariant'], ['primary', 'onPrimary'],
  ['primaryContainer', 'onPrimaryContainer'], ['secondaryContainer', 'onSecondaryContainer'], ['error', 'onError'],
];

function luminance(hex: string): number {
  const n = parseInt(hex.slice(1), 16);
  const c = [n >> 16, (n >> 8) & 255, n & 255].map(x => { x /= 255; return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; });
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
export function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

/** Text/background pairs under WCAG AA (4.5:1), e.g. "dark: primary/onPrimary 3.1". */
export function contrastWarnings(t: Theme): string[] {
  const out: string[] = [];
  for (const [mode, s] of Object.entries(t.schemes)) {
    for (const [bg, fg] of PAIRS) {
      if (!HEX.test(s[bg] ?? '') || !HEX.test(s[fg] ?? '')) continue; // derived roles aren't checked
      const r = contrast(s[bg], s[fg]);
      if (r < 4.5) out.push(`${mode}: ${fg} on ${bg} is ${r.toFixed(1)}:1`);
    }
  }
  return out;
}

/* ---------- import ---------- */

const HEX = /^#[0-9a-fA-F]{6}$/;

/** Parses a Material Theme Builder export ({schemes: {light, dark}}) or the same shape with fewer roles. */
export function parseTheme(json: string, name: string): Theme {
  let raw: { schemes?: Record<string, Record<string, unknown>>; name?: unknown; description?: unknown };
  try { raw = JSON.parse(json); } catch { throw new Error("That isn't valid JSON."); }
  const schemes: Theme['schemes'] = {};
  for (const mode of ['light', 'dark'] as const) {
    const src = raw.schemes?.[mode];
    if (!src) continue;
    const s: Scheme = {};
    for (const r of ROLES) {
      const v = String(src[r] ?? '');
      if (HEX.test(v)) s[r] = v.toUpperCase();
    }
    const missing = REQUIRED.filter(r => !s[r]);
    if (missing.length) throw new Error(`The ${mode} scheme needs ${missing.join(', ')} as #rrggbb.`);
    schemes[mode] = s;
  }
  if (!schemes.light && !schemes.dark) throw new Error('No "schemes.light" or "schemes.dark" found. Export JSON from Material Theme Builder.');
  const n = name.trim() || (typeof raw.name === 'string' && raw.name) || 'Imported theme';
  return { id: 'custom-' + Date.now().toString(36), name: n, schemes, custom: true };
}

/* ---------- persistence ---------- */
// ponytail: per browser (localStorage); move to the hub per user when the mobile app needs the same theme.

const K_PREF = 'walroos-theme', K_CUSTOM = 'walroos-themes', K_CSS = 'walroos-theme-css';

function read<T>(key: string, fallbackValue: T): T {
  try { const v = localStorage.getItem(key); return v ? JSON.parse(v) as T : fallbackValue; } catch { return fallbackValue; }
}
function write(key: string, value: unknown) {
  try { localStorage.setItem(key, typeof value === 'string' ? value : JSON.stringify(value)); } catch { /* private mode: theme just won't stick */ }
}

export const customThemes = (): Theme[] => read<Theme[]>(K_CUSTOM, []);
export const allThemes = (): Theme[] => [...PRESETS, ...customThemes()];
export function savedPref(): { id: string; mode: Mode } {
  const p = read<{ id?: string; mode?: Mode }>(K_PREF, {});
  return { id: p.id ?? 'walroos', mode: p.mode ?? 'system' };
}

/** Applies and remembers a theme. index.html re-applies the saved CSS before React loads (no flash). */
export function applyTheme(id: string, mode: Mode) {
  const t = allThemes().find(x => x.id === id) ?? PRESETS[0];
  const css = themeCSS(t, mode);
  document.documentElement.style.cssText = css;
  write(K_PREF, { id: t.id, mode });
  write(K_CSS, css);
}

export function addCustomTheme(t: Theme) { write(K_CUSTOM, [...customThemes(), t]); }
export function removeCustomTheme(id: string) { write(K_CUSTOM, customThemes().filter(t => t.id !== id)); }

/** Follow the OS when the mode is "system". */
export function watchSystemMode() {
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    const p = savedPref();
    if (p.mode === 'system') applyTheme(p.id, p.mode);
  });
}
