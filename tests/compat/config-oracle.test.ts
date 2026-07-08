import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { migrateV1ToV2 } from '../../src/config/migrate-v2';
import { loadRootConfig } from '../../src/config/profile-store';

const UPDATE = process.env.UPDATE_COMPAT_FIXTURES === '1';

describe('config compat oracle export', () => {
  it('exports config normalization fixtures from the TypeScript implementation', async () => {
    for (const fixture of configFixtures()) {
      await writeOrExpectJson(`testdata/compat/config/${fixture.name}.input.json`, fixture.input);
      const expected = fixture.kind === 'legacy'
        ? await normalizeLegacyFixture(fixture)
        : await normalizeRootFixture(fixture.input);
      await writeOrExpectJson(`testdata/compat/config/${fixture.name}.expected.json`, expected);
    }
  });
});

function configFixtures() {
  return [
    {
      name: 'minimal-root',
      kind: 'root' as const,
      input: {
        schemaVersion: 2,
        activeProfile: 'claude',
        preferences: { ignored: true },
        profiles: {
          claude: {
            schemaVersion: 2,
            agentKind: 'claude',
            accounts: {
              app: {
                id: 'cli_minimal',
                secret: 'plain-secret',
                tenant: 'feishu',
              },
            },
          },
        },
      },
    },
    {
      name: 'codex-profile',
      kind: 'root' as const,
      input: {
        schemaVersion: 2,
        activeProfile: 'codex',
        preferences: {},
        profiles: {
          codex: {
            schemaVersion: 2,
            agentKind: 'codex',
            accounts: {
              app: {
                id: 'cli_codex',
                secret: { source: 'env', id: 'LARK_APP_SECRET' },
                tenant: 'lark',
              },
            },
            preferences: {
              messageReply: 'card',
              showToolCalls: false,
              access: { allowedUsers: ['legacy-user'] },
              requireMentionInGroup: false,
            },
            access: {
              allowedUsers: ['ou_user', 7, 'ou_admin'],
              allowedChats: ['oc_chat'],
              admins: ['ou_admin'],
            },
            workspaces: {
              default: '  /workspace/project  ',
              trustedRoots: ['/legacy'],
            },
            sandbox: {
              defaultMode: 'read-only',
              maxMode: 'workspace-write',
            },
            permissions: {
              defaultAccess: 'workspace',
              maxAccess: 'full',
            },
            codex: {
              binaryPath: '/usr/local/bin/codex',
              version: '1.2.3',
              inheritCodexHome: false,
              ignoreUserConfig: true,
            },
            attachments: {
              maxCount: 3,
              maxBytes: 4096,
              imageMaxBytes: -1,
            },
            comments: { legacy: true },
            larkCli: {
              identityPreset: 'user-default',
              localUserImport: {
                status: 'imported',
                attemptedAt: '2026-07-01T00:00:00.000Z',
                importedAt: '2026-07-01T00:00:01.000Z',
              },
            },
          },
        },
      },
    },
    {
      name: 'legacy-single-profile',
      kind: 'legacy' as const,
      profile: 'claude',
      input: {
        accounts: {
          app: {
            id: 'cli_legacy',
            secret: { source: 'exec', provider: 'bridge', id: 'app-cli_legacy' },
            tenant: 'feishu',
          },
        },
        secrets: {
          providers: {
            bridge: {
              source: 'exec',
              command: '/opt/bridge/secrets-getter',
              args: [],
            },
          },
          defaults: {
            exec: 'bridge',
          },
        },
        preferences: {
          messageReply: 'text',
          messageReplyMigrated: false,
          showToolCalls: false,
          cotMessages: 'simple',
          requireMentionInGroup: false,
          access: {
            allowedUsers: ['ou_legacy', 42],
            allowedChats: ['oc_legacy'],
            admins: ['ou_admin'],
          },
        },
      },
    },
    {
      name: 'active-profile',
      kind: 'root' as const,
      input: {
        schemaVersion: 2,
        activeProfile: 'claude',
        preferences: {},
        secrets: {
          defaults: { env: 'default' },
          providers: {
            default: { source: 'env', allowlist: ['LARK_APP_SECRET'] },
          },
        },
        profiles: {
          claude: {
            schemaVersion: 2,
            agentKind: 'claude',
            accounts: {
              app: { id: 'cli_claude', secret: '${LARK_APP_SECRET}', tenant: 'feishu' },
            },
          },
          codex: {
            schemaVersion: 2,
            agentKind: 'codex',
            accounts: {
              app: { id: 'cli_codex_active', secret: '${LARK_APP_SECRET}', tenant: 'feishu' },
            },
            codex: { binaryPath: 'codex' },
          },
        },
      },
    },
  ];
}

async function normalizeRootFixture(input: unknown): Promise<unknown> {
  const root = await mkdtemp(join(tmpdir(), 'bridge-config-root-'));
  const configFile = join(root, 'config.json');
  await writeFile(configFile, `${JSON.stringify(input, null, 2)}\n`, 'utf8');
  const normalized = await loadRootConfig(configFile);
  expect(normalized).toBeDefined();
  return normalized;
}

async function normalizeLegacyFixture(fixture: { input: unknown; profile?: string }): Promise<unknown> {
  const root = await mkdtemp(join(tmpdir(), 'bridge-config-legacy-'));
  const configFile = join(root, 'config.json');
  await writeFile(configFile, `${JSON.stringify(fixture.input, null, 2)}\n`, 'utf8');
  await migrateV1ToV2({
    rootDir: root,
    configFile,
    profile: fixture.profile,
    agentKind: 'claude',
  });
  const normalized = await loadRootConfig(configFile);
  expect(normalized).toBeDefined();
  return normalized;
}

async function writeOrExpectJson(path: string, value: unknown): Promise<void> {
  const fullPath = join(process.cwd(), path);
  const payload = `${JSON.stringify(value, null, 2)}\n`;
  if (UPDATE) {
    await mkdir(dirname(fullPath), { recursive: true });
    await writeFile(fullPath, payload, 'utf8');
    return;
  }
  const existing = await readFile(fullPath, 'utf8');
  expect(existing).toBe(payload);
}
