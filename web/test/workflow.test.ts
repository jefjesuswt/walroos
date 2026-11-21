import { expect, test } from 'bun:test';
import type { App } from '../src/api';
import { githubWorkflow } from '../src/apps/workflow';

const app = (over: Partial<App>) => ({ name: 'shop', repo_url: 'https://github.com/me/shop', image: '', branch: '', ...over } as App);

test('a repo app tests, then deploys the commit that passed, on its branch', () => {
  const wf = githubWorkflow(app({ branch: 'release' }));
  expect(wf).toContain('branches: [release]');
  expect(wf).toContain('needs: test');
  expect(wf).toContain('/hooks/deploy/shop');
  expect(wf).toContain(`-d '{"sha": "\${{ github.sha }}"}'`);
  expect(wf).not.toContain('build-push-action');
});

test('an image app builds and pushes its tag, and deploys without a commit', () => {
  const wf = githubWorkflow(app({ repo_url: '', image: 'ghcr.io/me/shop:latest' }));
  expect(wf).toContain('registry: ghcr.io');
  expect(wf).toContain('tags: ghcr.io/me/shop:latest');
  expect(wf).not.toContain('github.sha');
});

test('the default branch is main', () => {
  expect(githubWorkflow(app({}))).toContain('branches: [main]');
});
