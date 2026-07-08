import { access, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const repoRoot = fileURLToPath(new URL('../../..', import.meta.url));

describe('README runtime contract', () => {
  it('documents maintained runtime surfaces in user-visible docs', async () => {
    const docs = await readDocs();

    for (const phrase of [
      'per-profile service',
      'workspaces.default',
      '/invite user',
      '/remove user',
      '/invite group',
      '/remove group',
      '/invite all group',
      'Windows',
      '.cmd',
      'profile export',
      'profile remove',
      '--purge --yes',
      '--include-secrets --yes',
      'lark-cli identity policy',
      'profile-local lark-cli directory',
      'lark-cli 身份策略',
      '当前 profile 的 lark-cli 目录',
      'pnpm test',
      'pnpm typecheck',
      'pnpm build',
      'pnpm test:go',
      'pnpm test:go:race',
      'pnpm test:go:cross',
      'pkg/bridge',
      'docs/go-sdk-usage.md',
      'docs/pkg/bridge.md',
    ]) {
      expect(docs).toContain(phrase);
    }
  });

  it('documents canonical Go SDK public API names', async () => {
    const docs = await readSDKDocs();

    for (const phrase of [
      'BootstrapProfileConfig',
      'StartProfileService',
      'NewProfileServiceController',
      'BuildLarkCLISourceProjection',
      'WriteLarkCLISourceProjection',
      'PreflightLarkCLI',
      'bridge.New',
      'CLI-equivalent',
    ]) {
      expect(docs).toContain(phrase);
    }
    for (const phrase of [
      'BuildLarkCliSourceProjection',
      'WriteLarkCliSourceProjection',
      'PreflightLarkCli',
    ]) {
      expect(docs).not.toContain(phrase);
    }
  });

  it('keeps README links aligned and covered by npm package files', async () => {
    const [en, zh, packageRaw] = await Promise.all([
      readFile(new URL('../../../README.md', import.meta.url), 'utf8'),
      readFile(new URL('../../../README.zh.md', import.meta.url), 'utf8'),
      readFile(new URL('../../../package.json', import.meta.url), 'utf8'),
    ]);
    const packageJSON = JSON.parse(packageRaw) as { files?: string[] };
    const packageFiles = packageJSON.files ?? [];

    expect(packageFiles).toEqual(expect.arrayContaining([
      'docs',
      'assets',
      'README.md',
      'README.zh.md',
      'LICENSE',
    ]));

    const enTargets = collectLocalReferences(en).filter((target) => target !== 'README.zh.md');
    const zhTargets = collectLocalReferences(zh).filter((target) => target !== 'README.md');
    expect(enTargets).toEqual(zhTargets);

    for (const target of new Set([...enTargets, ...zhTargets])) {
      await expectLocalFile(target);
      expect(isPackaged(target, packageFiles)).toBe(true);
    }
  });

  it('keeps CLI help aligned with profile-aware service and first-run workspace flags', async () => {
    const [cli, help, configCard] = await Promise.all([
      readFile(new URL('../../../src/cli/index.ts', import.meta.url), 'utf8'),
      readFile(new URL('../../../src/card/templates.ts', import.meta.url), 'utf8'),
      readFile(new URL('../../../src/card/config-card.ts', import.meta.url), 'utf8'),
    ]);

    expect(cli).toContain('--workspace <path>');
    expect(cli).toContain('profile name (defaults to active profile)');
    expect(cli).toContain('Archive a profile and its local state');
    expect(help).not.toContain('/doc ws');
    expect(configCard).not.toContain('`/doc`');
  });

  it('does not document trusted or allowed directory authorization', async () => {
    const docs = await readDocs();

    for (const phrase of [
      '允许访问目录',
      '可信目录',
      '安全目录',
      'trustedRoots',
      '/ws add',
      '/ws remove --root',
      'allowed directory',
      'allowed directories',
      'trusted directory',
      'safe directory',
    ]) {
      expect(docs).not.toContain(phrase);
    }
  });

  it('documents access control commands instead of config-only access management', async () => {
    const docs = await readDocs();

    expect(docs).not.toContain('`/config` only adjusts presentation preferences. Manage access in the profile config.');
    expect(docs).not.toContain('`/config` 只调整展示偏好，不再维护访问名单。请在 profile config 里维护。');
  });

  it('documents cloud-doc comments as document-scoped instead of access-gated', async () => {
    const docs = await readDocs();

    expect(docs).toContain('Cloud-doc comments are document-scoped');
    expect(docs).toContain('云文档评论按文档权限生效');
    expect(docs).not.toContain('comments.enabled');
    expect(docs).not.toContain('comments.rateLimit');
    expect(docs).not.toContain('/doc ws bind');
  });

  it('documents canonical permissions instead of recommending legacy sandbox config', async () => {
    const docs = await readDocs();

    expect(docs).toContain('"permissions"');
    expect(docs).toContain('"defaultAccess": "full"');
    expect(docs).toContain('"maxAccess": "full"');
    expect(docs).toContain('legacy `sandbox`');
    expect(docs).toContain('旧版 `sandbox`');
    expect(docs).not.toContain('"sandbox"');
  });
});

async function readDocs(): Promise<string> {
  const [en, zh] = await Promise.all([
    readFile(new URL('../../../README.md', import.meta.url), 'utf8'),
    readFile(new URL('../../../README.zh.md', import.meta.url), 'utf8'),
  ]);
  return `${en}\n${zh}`;
}

async function readSDKDocs(): Promise<string> {
  const [usage, facade] = await Promise.all([
    readFile(new URL('../../../docs/go-sdk-usage.md', import.meta.url), 'utf8'),
    readFile(new URL('../../../docs/pkg/bridge.md', import.meta.url), 'utf8'),
  ]);
  return `${usage}\n${facade}`;
}

function collectLocalReferences(markdown: string): string[] {
  const targets = new Set<string>();
  for (const match of markdown.matchAll(/\[[^\]]+\]\(([^)]+)\)/g)) {
    const rawTarget = match[1];
    if (!rawTarget) {
      continue;
    }
    const target = normalizeLocalTarget(rawTarget);
    if (target) {
      targets.add(target);
    }
  }
  for (const match of markdown.matchAll(/src="([^"]+)"/g)) {
    const rawTarget = match[1];
    if (!rawTarget) {
      continue;
    }
    const target = normalizeLocalTarget(rawTarget);
    if (target) {
      targets.add(target);
    }
  }
  return [...targets].sort();
}

function normalizeLocalTarget(target: string): string | undefined {
  if (/^(?:https?:|mailto:|#)/.test(target)) {
    return undefined;
  }
  const withoutFragment = target.split('#')[0]?.split('?')[0];
  if (!withoutFragment) {
    return undefined;
  }
  return withoutFragment.replace(/^\.\//, '');
}

async function expectLocalFile(target: string): Promise<void> {
  await expect(access(path.join(repoRoot, target))).resolves.toBeUndefined();
}

function isPackaged(target: string, packageFiles: string[]): boolean {
  return packageFiles.some((entry) => {
    const normalized = entry.replace(/\/$/, '');
    return target === normalized || target.startsWith(`${normalized}/`);
  });
}
