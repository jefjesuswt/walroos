// The GitHub Actions workflow the Automation tab offers: tests first, and the deploy only if they
// pass. An image app also builds and pushes its image to the registry its tag names.
import type { App } from '../api';

const deployStep = (app: App, sha: boolean) => `      - name: Deploy with walroos
        run: |
          curl -fsS -X POST "$WALROOS_URL/hooks/deploy/${app.name}" \\
            -H "Authorization: Bearer $WALROOS_DEPLOY_TOKEN"${sha ? ` \\
            -d '{"sha": "\${{ github.sha }}"}'` : ''}
        env:
          WALROOS_URL: \${{ secrets.WALROOS_URL }}
          WALROOS_DEPLOY_TOKEN: \${{ secrets.WALROOS_DEPLOY_TOKEN }}`;

const testJob = `  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      # Your tests. For example: npm ci && npm test, go test ./..., pytest
      - run: make test`;

export function githubWorkflow(app: App): string {
  const branch = app.branch || 'main';
  if (!app.image) {
    return `name: test and deploy
on:
  push:
    branches: [${branch}]

jobs:
${testJob}

  deploy:
    needs: test
    runs-on: ubuntu-latest
    steps:
${deployStep(app, true)}
`;
  }
  const [registry] = app.image.split('/');
  return `name: test, build and deploy
on:
  push:
    branches: [main]

jobs:
${testJob}

  deploy:
    needs: test
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with:
          registry: ${registry}
          username: \${{ github.actor }}
          password: \${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          push: true
          tags: ${app.image}
${deployStep(app, false)}
`;
}
