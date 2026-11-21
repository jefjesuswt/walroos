// bun run check: the UI rules from AGENTS.md that a machine can see. It fails on
//  - an inline style that sets anything but CSS custom properties (style={{ '--pct': … }} is fine),
//  - a hex color in a component or in CSS outside tokens.css (colors are M3 roles),
//  - a source file over 300 lines (split it: one component or one job per file).
import { Glob } from 'bun';

const MAX_LINES = 300;
const problems: string[] = [];

for await (const path of new Glob('src/**/*.{ts,tsx,css}').scan('.')) {
  const lines = (await Bun.file(path).text()).split('\n');
  if (lines.length > MAX_LINES) problems.push(`${path}: ${lines.length} lines (max ${MAX_LINES}): split it`);
  lines.forEach((line, i) => {
    const at = `${path}:${i + 1}`;
    if (path.endsWith('.tsx')) {
      for (const m of line.matchAll(/style=\{\{([^}]*)\}/g)) {
        const keys = [...m[1].matchAll(/(?:^|,)\s*\[?\s*'?([\w-]+)'?\s*\]?\s*:/g)].map(k => k[1]);
        if (keys.some(k => !k.startsWith('--'))) problems.push(`${at}: inline style (${keys.join(', ')}): use a class or a component prop`);
      }
      if (/#[0-9a-fA-F]{3,8}\b/.test(line.replace(/placeholder=(['"]).*?\1/g, '')) && /['"`]#/.test(line)) problems.push(`${at}: hex color: use an M3 role`);
    }
    if (path.endsWith('.css') && !path.endsWith('tokens.css') && /#[0-9a-fA-F]{3,8}\b/.test(line.replace(/\/\*.*?\*\//g, ''))) {
      problems.push(`${at}: hex color: use an M3 role (var(--md-*) or an alias in tokens.css)`);
    }
  });
}

if (problems.length) {
  console.error(problems.join('\n'));
  console.error(`\n${problems.length} problem(s). See web/src/ui/README.md.`);
  process.exit(1);
}
console.log('check: ok');
