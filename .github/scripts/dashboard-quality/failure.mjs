import { writeFile } from 'node:fs/promises';
import path from 'node:path';

export async function recordFailure(error, output) {
  // Report the primary failure before attempting ancillary diagnostics.
  console.error(error.message);
  process.exitCode = 1;
  try {
    await writeFile(path.join(output, 'failure.json'), JSON.stringify({ error: error.message }, null, 2) + '\n');
  } catch (reportError) {
    console.error(`Failure report unavailable: ${reportError.message}`);
  }
}
