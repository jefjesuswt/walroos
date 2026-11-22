// A command typed on one line, as a shell would split it: spaces separate arguments, "..." and
// '...' keep spaces, \ escapes the next character. No variables or globs: the container gets the
// arguments as they are.
export function splitCommand(line: string): string[] {
  const args: string[] = [];
  let cur = '', inArg = false, quote = '';
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === quote) quote = '';
      else if (c === '\\' && quote === '"' && i + 1 < line.length) cur += line[++i];
      else cur += c;
    } else if (c === '"' || c === "'") { quote = c; inArg = true; }
    else if (c === '\\' && i + 1 < line.length) { cur += line[++i]; inArg = true; }
    else if (/\s/.test(c)) { if (inArg) { args.push(cur); cur = ''; inArg = false; } }
    else { cur += c; inArg = true; }
  }
  if (inArg) args.push(cur);
  return args;
}

// joinCommand writes args back on one line, quoting what splitCommand would split.
export const joinCommand = (args: string[]) =>
  args.map(a => a !== '' && /^[\w@%+=:,./-]+$/.test(a) ? a : `"${a.replace(/(["\\])/g, '\\$1')}"`).join(' ');
