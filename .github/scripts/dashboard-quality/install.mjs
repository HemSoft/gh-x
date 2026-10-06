import { spawnSync } from 'node:child_process';
import { copyFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { assertExternalToolsDirectory, repositoryRoot, toolsDirectory } from './tools.mjs';

let output = await assertExternalToolsDirectory(await toolsDirectory());
await mkdir(output, { recursive: true });
output = await assertExternalToolsDirectory(output);
for (const name of ['package.json', 'package-lock.json']) {
  await copyFile(path.join(repositoryRoot, name), path.join(output, name));
}
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
const result = spawnSync(npm, ['ci', '--ignore-scripts', '--no-fund', '--no-audit'], {
  cwd: output, stdio: 'inherit', shell: process.platform === 'win32',
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
if (result.status === 0) {
  console.log(`Isolated dashboard quality tools: ${output}`);
} else {
  console.error(`Locked npm ci failed: ${result.signal ? `signal ${result.signal}` : `exit ${result.status}`}; tools remain incomplete at ${output}`);
}
