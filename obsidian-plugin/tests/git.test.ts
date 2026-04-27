import * as os from 'os';
import * as path from 'path';
import * as fsp from 'fs/promises';
import * as fs from 'fs';
import git from 'isomorphic-git';

// Mock network operations; keep all local filesystem operations real
jest.mock('isomorphic-git', () => ({
  ...jest.requireActual('isomorphic-git'),
  clone: jest.fn().mockResolvedValue({}),
  pull: jest.fn().mockResolvedValue({}),
  push: jest.fn().mockResolvedValue({ ok: true }),
}));

import { GitService } from '../src/git';
import { PubObsSettings, FolderRepo } from '../src/settings';

const SETTINGS: PubObsSettings = {
  defaultUsername: 'testuser',
  defaultPat: 'testpat',
  defaultBranch: 'main',
  autoSync: false,
  repos: [],
};

async function makeTestRepo(): Promise<{
  vaultPath: string;
  folderPath: string;
  repo: FolderRepo;
  service: GitService;
  cleanup: () => Promise<void>;
}> {
  const vaultPath = await fsp.mkdtemp(path.join(os.tmpdir(), 'pubobs-test-'));
  const folderPath = 'notes';
  const dir = path.join(vaultPath, folderPath);
  await fsp.mkdir(dir, { recursive: true });

  await git.init({ fs, dir, defaultBranch: 'main' });
  await fsp.writeFile(path.join(dir, 'README.md'), '# Test');
  await git.add({ fs, dir, filepath: 'README.md' });
  await git.commit({
    fs,
    dir,
    message: 'initial',
    author: { name: 'test', email: 'test@test.com' },
  });

  const repo: FolderRepo = { folderPath, remoteUrl: 'https://example.com/test.git' };
  const service = new GitService(vaultPath, () => SETTINGS);
  const cleanup = () => fsp.rm(vaultPath, { recursive: true, force: true });
  return { vaultPath, folderPath, repo, service, cleanup };
}

describe('GitService', () => {
  beforeEach(() => jest.clearAllMocks());

  it('clone() calls git.clone with the correct dir and url', async () => {
    const mockGit = require('isomorphic-git') as jest.Mocked<typeof import('isomorphic-git')>;
    const vaultPath = await fsp.mkdtemp(path.join(os.tmpdir(), 'pubobs-clone-'));
    try {
      const service = new GitService(vaultPath, () => SETTINGS);
      const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

      await service.clone(repo);

      expect(mockGit.clone).toHaveBeenCalledWith(expect.objectContaining({
        url: 'https://example.com/repo.git',
        dir: path.join(vaultPath, 'notes'),
      }));
    } finally {
      await fsp.rm(vaultPath, { recursive: true, force: true });
    }
  });

  it('pull() calls git.pull with the correct dir', async () => {
    const mockGit = require('isomorphic-git') as jest.Mocked<typeof import('isomorphic-git')>;
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    await service.pull(repo);

    expect(mockGit.pull).toHaveBeenCalledWith(expect.objectContaining({
      dir: path.join('/vault', 'notes'),
    }));
  });

  it('stage() stages modified and new files, returns their paths', async () => {
    const { vaultPath, folderPath, repo, service, cleanup } = await makeTestRepo();
    try {
      await fsp.writeFile(path.join(vaultPath, folderPath, 'README.md'), '# Modified');
      await fsp.writeFile(path.join(vaultPath, folderPath, 'note.md'), '# New note');

      const staged = await service.stage(repo);

      expect(staged).toContain('README.md');
      expect(staged).toContain('note.md');
    } finally {
      await cleanup();
    }
  });

  it('commit() creates a commit with the given message and returns the sha', async () => {
    const { vaultPath, folderPath, repo, service, cleanup } = await makeTestRepo();
    try {
      await fsp.writeFile(path.join(vaultPath, folderPath, 'note.md'), '# Note');
      await service.stage(repo);

      const sha = await service.commit(repo, 'pubobs: sync 2026-04-26T12:00:00.000Z');

      expect(sha).toBeTruthy();
      const log = await git.log({ fs, dir: path.join(vaultPath, folderPath) });
      expect(log[0].commit.message.trim()).toBe('pubobs: sync 2026-04-26T12:00:00.000Z');
    } finally {
      await cleanup();
    }
  });

  it('commit() returns null and creates no commit when nothing is staged', async () => {
    const { vaultPath, folderPath, repo, service, cleanup } = await makeTestRepo();
    try {
      const logBefore = await git.log({ fs, dir: path.join(vaultPath, folderPath) });

      const sha = await service.commit(repo, 'pubobs: sync 2026-04-26T12:00:00.000Z');

      expect(sha).toBeNull();
      const logAfter = await git.log({ fs, dir: path.join(vaultPath, folderPath) });
      expect(logAfter).toHaveLength(logBefore.length);
    } finally {
      await cleanup();
    }
  });

  it('push() calls git.push with the correct dir', async () => {
    const mockGit = require('isomorphic-git') as jest.Mocked<typeof import('isomorphic-git')>;
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    await service.push(repo);

    expect(mockGit.push).toHaveBeenCalledWith(expect.objectContaining({
      dir: path.join('/vault', 'notes'),
    }));
  });

  it('testConnection() returns ok:true when getRemoteInfo2 succeeds', async () => {
    const mockGit = require('isomorphic-git') as any;
    mockGit.getRemoteInfo2 = jest.fn().mockResolvedValue({ capabilities: [] });
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    const result = await service.testConnection(repo);

    expect(result.ok).toBe(true);
  });

  it('testConnection() returns ok:false when getRemoteInfo2 throws', async () => {
    const mockGit = require('isomorphic-git') as any;
    mockGit.getRemoteInfo2 = jest.fn().mockRejectedValue(new Error('403 Forbidden'));
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    const result = await service.testConnection(repo);

    expect(result.ok).toBe(false);
    expect(result.message).toContain('403');
  });
});
