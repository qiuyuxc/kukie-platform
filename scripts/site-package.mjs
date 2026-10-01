import { spawn } from 'node:child_process';
import { mkdir, cp, writeFile } from 'node:fs/promises';
import { resolve, dirname, basename } from 'node:path';

const repository = resolve(import.meta.dirname, '..');
if (!process.env.KUKIE_SITE_URL) throw new Error('Set KUKIE_SITE_URL to the intended site origin before packaging');
const stamp = new Date().toISOString().replace(/[:.]/g, '-');
const release = resolve(repository, process.env.KUKIE_RELEASE_OUTPUT || `.local/releases/kukie-${stamp}`);
async function run(command, args, cwd = repository, env = process.env) {
  await new Promise((resolveRun, reject) => {
    const child = spawn(command, args, { cwd, env, stdio: 'inherit' });
    child.on('error', reject);
    child.on('exit', code => code === 0 ? resolveRun() : reject(new Error(`${command} exited with ${code}`)));
  });
}
await mkdir(dirname(release), { recursive: true });
await mkdir(release);
await run(process.execPath, ['scripts/site.mjs'], repository, { ...process.env, KUKIE_SITE_OUTPUT: resolve(release, 'site') });
await run('npm', ['ci'], resolve(repository, 'apps/console'));
await run('npm', ['run', 'build', '--', '--outDir', resolve(release, 'apps/console/dist')], resolve(repository, 'apps/console'));
await mkdir(resolve(release, 'bin'), { recursive: true });
await run('go', ['build', '-trimpath', '-o', resolve(release, 'bin/kukie-api'), '.'], resolve(repository, 'services/api'));
await cp(resolve(repository, 'content/posts'), resolve(release, 'content/posts'), { recursive: true });
await cp(resolve(repository, 'static'), resolve(release, 'static'), { recursive: true });
await cp(resolve(repository, 'deploy/go-site'), resolve(release, 'deploy'), { recursive: true });
await cp(resolve(repository, 'docs/go-site.md'), resolve(release, 'README.md'));
await writeFile(resolve(release, 'build.json'), JSON.stringify({ built_at: new Date().toISOString(), site_url: process.env.KUKIE_SITE_URL, platform: process.platform, architecture: process.arch }, null, 2) + '\n');
await run('tar', ['-czf', release + '.tar.gz', '-C', dirname(release), basename(release)]);
console.log(`Release: ${release}\nArchive: ${release}.tar.gz\nBuilt for this host. No database, credentials, or live binaries were copied.`);
