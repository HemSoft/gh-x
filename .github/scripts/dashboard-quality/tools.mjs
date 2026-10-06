import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const repositoryRoot = fileURLToPath(new URL('../../../', import.meta.url));

export async function toolsDirectory(root = repositoryRoot) {
  if (process.env.GH_X_DASHBOARD_QUALITY_TOOLS) return path.resolve(process.env.GH_X_DASHBOARD_QUALITY_TOOLS);
  const lock = await readFile(path.join(root, 'package-lock.json'));
  const digest = createHash('sha256').update(lock).digest('hex');
  return path.join(os.tmpdir(), `gh-x-dashboard-quality-${digest}`);
}
