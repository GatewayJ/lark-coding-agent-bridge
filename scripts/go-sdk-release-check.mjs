import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const modulePath = 'github.com/zarazhangrui/lark-coding-agent-bridge';
const sdkImportPath = `${modulePath}/pkg/bridge`;
const modes = new Set(process.argv.slice(2));
const allowedModes = new Set(['--tracked', '--head', '--published']);
const sdkPathspecs = [
  'go.mod',
  'go.sum',
  'cmd/lark-channel-bridge',
  'docs/go-sdk-migration-plan.md',
  'docs/go-sdk-usage.md',
  'docs/pkg/bridge.md',
  'internal',
  'pkg/bridge',
  'scripts/go-cross-check.mjs',
  'scripts/go-sdk-release-check.mjs',
  'testdata/compat',
  'tests/compat',
  'tests/external_go_smoke',
];

for (const mode of modes) {
  if (!allowedModes.has(mode)) {
    console.error(`Unknown Go SDK release check option: ${mode}`);
    console.error(`Usage: node scripts/go-sdk-release-check.mjs [${[...allowedModes].join('|')}]`);
    process.exit(1);
  }
}

if (modes.size === 0 || modes.has('--tracked')) {
  verifyTracked();
}
if (modes.has('--head')) {
  verifyHead();
}
if (modes.has('--published')) {
  verifyPublished();
}

function verifyTracked() {
  const tracked = gitLines(['ls-files', '--', ...sdkPathspecs]);
  if (tracked.length === 0) {
    fail('Go SDK release check failed: no SDK files are tracked by Git.');
  }
  const missing = sdkPathspecs.filter((pathspec) => gitLines(['ls-files', '--', pathspec]).length === 0);
  if (missing.length > 0) {
    failWithList('Go SDK release check failed: required pathspecs are not tracked by Git:', missing);
  }

  const untracked = gitLines(['ls-files', '--others', '--exclude-standard', '--', ...sdkPathspecs]);
  if (untracked.length > 0) {
    failWithList('Go SDK release check failed: SDK files exist but are not tracked by Git:', untracked);
  }
}

function verifyHead() {
  const missing = sdkPathspecs.filter((pathspec) => gitLines(['ls-tree', '-r', '--name-only', 'HEAD', '--', pathspec]).length === 0);
  if (missing.length > 0) {
    failWithList('Go SDK release check failed: required pathspecs are not present in HEAD:', missing);
  }
}

function verifyPublished() {
  const version = process.env.GO_SDK_IMPORT_VERSION || 'latest';
  const replaceModule = process.env.GO_SDK_REPLACE_MODULE;
  const replaceVersion = process.env.GO_SDK_REPLACE_VERSION || version;
  const workdir = mkdtempSync(join(tmpdir(), 'lark-channel-go-sdk-published-'));
  try {
    run('go', ['mod', 'init', 'published-smoke.example'], workdir);
    if (replaceModule) {
      run('go', ['mod', 'edit', '-require', `${modulePath}@v0.0.0`], workdir);
      run('go', ['mod', 'edit', '-replace', `${modulePath}=${replaceModule}@${replaceVersion}`], workdir);
    } else {
      run('go', ['get', `${sdkImportPath}@${version}`], workdir);
    }
    writeFileSync(
      join(workdir, 'bridge_smoke_test.go'),
      `package smoke

import (
  "testing"

  bridge "${sdkImportPath}"
)

func TestPublishedBridgeSDKImports(t *testing.T) {
  if bridge.BridgeSystemPrompt(nil) == "" {
    t.Fatal("BridgeSystemPrompt returned empty content")
  }
}
`,
    );
    if (replaceModule) {
      run('go', ['mod', 'tidy'], workdir);
    }
    run('go', ['test', './...'], workdir);
  } finally {
    rmSync(workdir, { recursive: true, force: true });
  }
}

function gitLines(args) {
  const result = spawnSync('git', args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  if (result.status !== 0) {
    fail(result.stderr.trim() || `git ${args.join(' ')} failed`);
  }
  return result.stdout.split(/\r?\n/).filter(Boolean);
}

function run(command, commandArgs, cwd) {
  const result = spawnSync(command, commandArgs, {
    cwd,
    stdio: 'inherit',
    env: process.env,
  });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

function failWithList(message, values) {
  console.error(message);
  for (const value of values) {
    console.error(`- ${value}`);
  }
  process.exit(1);
}

function fail(message) {
  console.error(message);
  process.exit(1);
}
