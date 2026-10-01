import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const mode = process.argv[2];
async function run(command, args, cwd = root, env = process.env) {
  await new Promise((resolveRun, reject) => {
    const child = spawn(command, args, { cwd, env, stdio: 'inherit' });
    child.on('error', reject);
    child.on('exit', code => code === 0 ? resolveRun() : reject(new Error(`${command} exited with ${code}`)));
  });
}

if (mode === 'build') {
  await mkdir(resolve(root, '.local/bin'), { recursive: true });
  await run(process.execPath, ['scripts/site.mjs']);
  await run('npm', ['ci'], resolve(root, 'apps/console'));
  await run('npm', ['run', 'build'], resolve(root, 'apps/console'));
  await run('go', ['build', '-trimpath', '-o', resolve(root, '.local/bin/kukie-api'), '.'], resolve(root, 'services/api'));
} else if (mode === 'start') {
  await run(resolve(root, '.local/bin/kukie-api'), [], root, {
    ...process.env,
    KUKIE_REPOSITORY: root,
    KUKIE_SITE_DIR: process.env.KUKIE_SITE_DIR || resolve(root, '.local/site'),
  });
} else if (mode === 'test') {
  await run('go', ['test', '-count=1', './...'], resolve(root, 'services/api'));
  await run('go', ['vet', './...'], resolve(root, 'services/api'));
} else {
  throw new Error('Usage: node scripts/platform.mjs build|start|test');
}
