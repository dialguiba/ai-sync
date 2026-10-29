import { access } from 'node:fs/promises';
import { constants } from 'node:fs';
import { spawn, execFile } from 'node:child_process';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

function orcaCommand() {
  return process.platform === 'linux' ? 'orca-ide' : 'orca';
}

async function orcaJSON(args) {
  const { stdout } = await execFileAsync(orcaCommand(), [...args, '--json'], {
    timeout: 15000,
    maxBuffer: 8 * 1024 * 1024,
  });
  const response = JSON.parse(stdout);
  if (!response.ok) throw new Error(response.error?.message || 'Orca CLI failed');
  return response.result;
}

async function projectForContext(context) {
  for (const { id } of context.terminals) {
    let terminal;
    try {
      ({ terminal } = await orcaJSON(['terminal', 'show', '--terminal', id]));
    } catch {
      // A terminal can disappear between reading the context and querying it.
      continue;
    }
    if (terminal?.worktreePath) {
      if (terminal.executionHostId !== 'local') {
        throw new Error('The catalog currently needs a local Orca worktree.');
      }
      return terminal.worktreePath;
    }
  }
  const { worktrees } = await orcaJSON(['worktree', 'list']);
  const matches = worktrees.filter(worktree =>
    worktree.displayName === context.displayName && worktree.branch === context.branch
  );
  if (matches.length === 1 && matches[0].path) {
    if (matches[0].hostId !== 'local') {
      throw new Error('The catalog currently needs a local Orca worktree.');
    }
    return matches[0].path;
  }
  throw new Error('Could not identify the focused worktree. Open a terminal in it and retry.');
}

async function aiSyncCommand() {
  const candidates = [
    'ai-sync',
    join(homedir(), 'go', 'bin', process.platform === 'win32' ? 'ai-sync.exe' : 'ai-sync'),
  ];
  for (const candidate of candidates) {
    try {
      if (candidate !== 'ai-sync') await access(candidate, constants.X_OK);
      await execFileAsync(candidate, ['catalog', '--help'], { timeout: 5000 });
      return candidate;
    } catch {
      // Try the next conventional installation location.
    }
  }
  throw new Error('ai-sync with the catalog command is unavailable. Install the current ai-sync build first.');
}

function launchCatalog(binary, project) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, ['catalog', '--orca', project], {
      cwd: project,
      detached: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    const timer = setTimeout(() => fail(new Error('ai-sync catalog did not start in time')), 15000);
    function fail(error) {
      clearTimeout(timer);
      child.kill();
      reject(error);
    }
    child.on('error', fail);
    child.on('exit', code => fail(new Error(stderr.trim() || `ai-sync catalog exited with code ${code}`)));
    child.stdout.on('data', chunk => {
      stdout += chunk.toString();
      if (!stdout.includes('\n')) return;
      clearTimeout(timer);
      child.removeAllListeners('exit');
      child.removeAllListeners('error');
      child.stdout.destroy();
      child.stderr.destroy();
      child.unref();
      resolve(stdout.trim());
    });
    child.stderr.on('data', chunk => { stderr += chunk.toString().slice(0, 4096); });
  });
}

export default async function activate(orca) {
  orca.commands.register('open-catalog', async () => {
    const context = await orca.host.call('workspace.readContext');
    if (!context) throw new Error('Focus an Orca worktree before opening the catalog.');
    const project = await projectForContext(context);
    const binary = await aiSyncCommand();
    return launchCatalog(binary, project);
  });
}
