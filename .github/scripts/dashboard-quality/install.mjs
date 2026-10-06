import { spawnSync } from 'node:child_process';
import { copyFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { repositoryRoot, toolsDirectory } from './tools.mjs';

const output = await toolsDirectory();
const relative = path.relative(repositoryRoot, output);
if (!relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)) throw new Error('Quality tools must be installed outside the source checkout');
await mkdir(output, { recursive: true });
for (const name of ['package.json', 'package-lock.json']) {
  await copyFile(path.join(repositoryRoot, name), path.join(output, name));
}
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
const result = spawnSync(npm, ['ci', '--ignore-scripts', '--no-fund', '--no-audit'], {
  cwd: output, stdio: 'inherit', shell: process.platform === 'win32',
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
console.log(`Isolated dashboard quality tools: ${output}`);
