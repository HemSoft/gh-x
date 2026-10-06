import { readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export async function rawFunctions(directory) {
  const byFile = new Map();
  for (const name of await readdir(directory)) {
    if (!name.endsWith('.json')) continue;
    const data = JSON.parse(await readFile(path.join(directory, name), 'utf8'));
    for (const script of data.result ?? []) {
      if (!script.url.startsWith('file:')) continue;
      const file = fileURLToPath(script.url);
      const functions = byFile.get(file) ?? new Map();
      byFile.set(file, functions);
      // V8's first entry is script execution, even when its offsets coincide
      // with an uncalled function spanning the entire file.
      for (const fn of script.functions.slice(1)) {
        const range = fn.ranges[0];
        const key = `${fn.functionName}:${range.startOffset}:${range.endOffset}`;
        const merged = functions.get(key) ?? { ...range, functionName: fn.functionName, count: 0 };
        merged.count += range.count;
        functions.set(key, merged);
      }
    }
  }
  return byFile;
}
