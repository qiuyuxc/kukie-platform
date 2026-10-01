import { spawn } from 'node:child_process';
import { cp, mkdir, readFile, writeFile, readdir, mkdtemp, rm } from 'node:fs/promises';
import { resolve, relative, extname, join } from 'node:path';
import { tmpdir } from 'node:os';

const root = resolve(import.meta.dirname, '..');
const output = resolve(root, process.env.KUKIE_SITE_OUTPUT || '.local/site');
const temporary = await mkdtemp(join(tmpdir(), 'kukie-site-'));
const extensions = new Set(['.css', '.js', '.json', '.wasm', '.png', '.jpg', '.jpeg', '.webp', '.gif', '.avif', '.ico', '.svg', '.woff', '.woff2', '.ttf', '.otf', '.mp4', '.webm', '.ogg', '.mp3']);
const base = process.env.KUKIE_SITE_URL;
const args = ['--config', 'hugo.toml,hugo.server.toml', '--destination', temporary, '--minify'];
if (base) {
  const origin = new URL(base);
  if (!['https:', 'http:'].includes(origin.protocol) || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) throw new Error('KUKIE_SITE_URL must be an HTTP(S) origin');
  args.push('--baseURL', origin.origin + '/');
}
try {
  await new Promise((resolveRun, reject) => {
    const child = spawn(process.env.HUGO_BIN || 'hugo', args, { cwd: root, stdio: 'inherit' });
    child.on('error', reject);
    child.on('exit', code => code === 0 ? resolveRun() : reject(new Error(`Hugo exited with ${code}`)));
  });
  const manifest = JSON.parse(await readFile(resolve(temporary, '_server/site.json'), 'utf8'));
  if (manifest.version !== 1 || !manifest.head || !manifest.hero) throw new Error('Site manifest is incomplete');
  const staticFiles = new Set(manifest.static_routes.map(route => route.endsWith('/') ? route.slice(1) + 'index.html' : route.slice(1)));
  const assets = [];
  const publicDirectory = resolve(output, 'public');
  await mkdir(publicDirectory, { recursive: true });
  async function copyPublic(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      if (entry.name.startsWith('.') || entry.name === '_server' || entry.isSymbolicLink()) continue;
      const source = join(directory, entry.name);
      if (entry.isDirectory()) { await copyPublic(source); continue; }
      const name = relative(temporary, source).split('\\').join('/');
      if (!staticFiles.has(name) && !extensions.has(extname(name).toLowerCase())) continue;
      if (name === 'index.json' || name === 'search/index.json') continue;
      await mkdir(resolve(publicDirectory, name, '..'), { recursive: true });
      await cp(source, resolve(publicDirectory, name));
      if (!staticFiles.has(name)) assets.push('/' + name);
    }
  }
  await copyPublic(temporary);
  await cp(resolve(root, 'apps/site/assets'), resolve(publicDirectory, 'site-assets'), { recursive: true });
  for (const entry of await readdir(resolve(root, 'apps/site/assets'))) assets.push('/site-assets/' + entry);
  await mkdir(resolve(output, '_server'), { recursive: true });
  await cp(resolve(root, 'apps/site/templates'), resolve(output, '_server/templates'), { recursive: true });
  manifest.assets = assets;
  await writeFile(resolve(output, '_server/site.json'), JSON.stringify(manifest));
  console.log(`Dynamic site bundle: ${output}`);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
