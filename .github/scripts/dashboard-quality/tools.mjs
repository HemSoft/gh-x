import { createHash } from 'node:crypto';
import { readFile, realpath } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const repositoryRoot = fileURLToPath(new URL('../../../', import.meta.url));

async function canonicalPath(file) {
  try {
    return await realpath(file);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    return path.join(await canonicalPath(path.dirname(file)), path.basename(file));
  }
}

export async function assertExternalToolsDirectory(directory) {
  const destination = await canonicalPath(directory);
  const relative = path.relative(await realpath(repositoryRoot), destination);
  if (!relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)) {
    throw new Error('Quality tools must be installed outside the source checkout');
  }
  return destination;
}

export async function toolsDirectory(root = repositoryRoot) {
  if (process.env.GH_X_DASHBOARD_QUALITY_TOOLS) return path.resolve(process.env.GH_X_DASHBOARD_QUALITY_TOOLS);
  const lock = await readFile(path.join(root, 'package-lock.json'));
  const digest = createHash('sha256').update(lock).digest('hex');
  return path.join(os.tmpdir(), `gh-x-dashboard-quality-${digest}`);
}
