/**
 * Exports the web app and stamps the service worker.
 *
 *   npm run build:web            # into dist/
 *   OUT=/srv/btcusd/web npm run build:web
 *
 * # Why this is a script and not just `expo export`
 *
 * The worker needs two things the export decides: a build identity, and the
 * list of files this build actually emitted. Hand-maintaining either is the
 * classic PWA failure — a worker whose version never changes never updates,
 * and a precache list that names last build's bundle caches a 404.
 *
 * Both are derived here from the output, so they cannot drift from it.
 */
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync } from 'node:fs';
import { readdir, readFile, writeFile, rm } from 'node:fs/promises';
import path from 'node:path';

const OUT = process.env.OUT ?? 'dist';

/**
 * Refuse before invoking Expo when the dependencies are not installed.
 *
 * # Why this is worth a check of its own
 *
 * Without it, `npx` does not fail — it offers to *download* the Expo CLI, and
 * a CLI fetched on its own cannot resolve this project's SDK version. The
 * platform inference then comes back without web and it reports:
 *
 *     No platforms are configured to use the Metro bundler in the project
 *     Expo config.
 *
 * Which sends whoever reads it to app.json, where nothing is wrong. This has
 * now misdirected two people, so it is worth the six lines to say the real
 * thing instead.
 */
if (!existsSync('node_modules/expo')) {
  console.error('mobile/node_modules is missing or incomplete: no node_modules/expo.\n');
  console.error('Run:\n');
  console.error('    npm install\n');
  console.error('Without it npx downloads a detached Expo CLI, which cannot read this');
  console.error("project's config and reports a platform problem that does not exist.");
  process.exit(1);
}

await rm(OUT, { recursive: true, force: true });

console.log(`exporting to ${OUT}`);
try {
  // --no-install so this can never silently reach for a CLI off the registry.
  // The check above should have caught that already; this makes it impossible
  // rather than merely reported.
  execFileSync('npx', ['--no-install', 'expo', 'export', '--platform', 'web', '--output-dir', OUT], {
    stdio: 'inherit',
  });
} catch {
  // Expo has already printed why. Adding a Node stack trace on top buries it
  // under twenty lines of internals, which is where the useful message goes to
  // die — the export failing is an ordinary thing to have to read.
  console.error('\nexport failed; see the error above');
  process.exit(1);
}

/** Every file in the export, as absolute-from-root URLs. */
async function walk(dir, prefix = '') {
  const found = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const rel = `${prefix}/${entry.name}`;
    if (entry.isDirectory()) found.push(...(await walk(path.join(dir, entry.name), rel)));
    else found.push(rel);
  }
  return found;
}

const files = (await walk(OUT)).sort();

// A build that emits nothing is not a build. Expo has exited zero with an
// empty directory before now — a cache in a bad state, a bundler crash it
// swallowed — and the next thing to happen would be a service worker
// precaching a list of files that are not there.
for (const required of ['/index.html', '/sw.js']) {
  if (!files.includes(required)) {
    console.error(`\nthe export has no ${required}; it produced ${files.length} files`);
    process.exit(1);
  }
}

/**
 * What the worker precaches on install.
 *
 * The entry document and the bundle, which is the whole app, plus the icons an
 * install needs. Not every file: the export also carries metadata.json and the
 * favicon, which nothing needs before the first paint, and a precache list
 * that fails halfway leaves the app with no worker at all.
 */
const shell = [
  '/',
  ...files.filter(
    (file) =>
      file.startsWith('/_expo/') ||
      file === '/manifest.json' ||
      /^\/(icon-\d+|apple-touch-icon)\.png$/.test(file),
  ),
];

/**
 * The build identity.
 *
 * A hash of the file list rather than a timestamp, so that rebuilding the same
 * source twice produces the same worker — a version that changes when nothing
 * did would make every deployment look like an update to anybody watching.
 */
const build = createHash('sha256').update(files.join('\n')).digest('hex').slice(0, 12);

const swPath = path.join(OUT, 'sw.js');
const worker = (await readFile(swPath, 'utf8'))
  .replace('__BUILD__', build)
  .replace("'__SHELL__'", shell.map((url) => JSON.stringify(url)).join(', '));

if (worker.includes('__BUILD__') || worker.includes('__SHELL__')) {
  throw new Error('the service worker was not stamped; its placeholders have moved');
}
await writeFile(swPath, worker);

console.log(`\nbuild ${build}`);
console.log(`shell ${shell.length} files:`);
for (const url of shell) console.log(`  ${url}`);
