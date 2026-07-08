import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const targets = [
  { goos: 'windows', goarch: 'amd64', ext: '.exe' },
  { goos: 'darwin', goarch: 'amd64', ext: '' },
  { goos: 'linux', goarch: 'amd64', ext: '' },
];

for (const target of targets) {
  const output = join(
    tmpdir(),
    `lark-channel-bridge-${target.goos}-${target.goarch}.test${target.ext}`,
  );
  const result = spawnSync(
    'go',
    ['test', '-mod=readonly', '-c', '-o', output, './cmd/lark-channel-bridge'],
    {
      stdio: 'inherit',
      env: {
        ...process.env,
        GOOS: target.goos,
        GOARCH: target.goarch,
      },
    },
  );
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
