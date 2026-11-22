import { describe, expect, test } from 'bun:test';
import { joinCommand, splitCommand } from '../src/apps/command';

describe('splitCommand', () => {
  test.each([
    ['node worker.js --queue mail', ['node', 'worker.js', '--queue', 'mail']],
    [`sh -c "echo \\"hi\\" && sleep 5"`, ['sh', '-c', 'echo "hi" && sleep 5']],
    [`printf '%s\\n' a`, ['printf', '%s\\n', 'a']],
    ['  spaced   out  ', ['spaced', 'out']],
    ['say ""', ['say', '']],
    ['', []],
  ])('%s', (line, want) => expect(splitCommand(line)).toEqual(want));
  test('joinCommand round-trips', () => {
    for (const args of [['sh', '-c', 'echo "a b" \\ c'], ['x', ''], ['node', 'a.js']]) expect(splitCommand(joinCommand(args))).toEqual(args);
  });
});
