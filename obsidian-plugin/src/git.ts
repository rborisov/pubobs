import git from 'isomorphic-git';
import http from 'isomorphic-git/http/node';
import * as fs from 'fs';
import * as fsp from 'fs/promises';
import * as path from 'path';
import { FolderRepo, PubObsSettings } from './settings';

export class GitService {
  constructor(
    private vaultPath: string,
    private getSettings: () => PubObsSettings,
    private fsImpl: typeof fs = fs,
    private httpImpl: typeof http = http,
  ) {}

  private get settings(): PubObsSettings { return this.getSettings(); }

  private repoDir(repo: FolderRepo): string {
    return path.join(this.vaultPath, repo.folderPath);
  }

  private resolveAuth(repo: FolderRepo): { username: string; password: string } {
    return {
      username: repo.username ?? this.settings.defaultUsername,
      password: repo.pat ?? this.settings.defaultPat,
    };
  }

  private resolveBranch(repo: FolderRepo): string {
    return repo.branch ?? this.settings.defaultBranch;
  }

  async clone(repo: FolderRepo): Promise<void> {
    const { username, password } = this.resolveAuth(repo);
    const dir = this.repoDir(repo);
    await fsp.mkdir(dir, { recursive: true });
    await git.clone({
      fs: this.fsImpl,
      http: this.httpImpl,
      dir,
      url: repo.remoteUrl,
      ref: this.resolveBranch(repo),
      singleBranch: true,
      onAuth: () => ({ username, password }),
    });
  }

  async hasCommits(repo: FolderRepo): Promise<boolean> {
    try {
      const log = await git.log({ fs: this.fsImpl, dir: this.repoDir(repo), depth: 1 });
      return log.length > 0;
    } catch {
      return false;
    }
  }

  async pull(repo: FolderRepo): Promise<void> {
    const { username, password } = this.resolveAuth(repo);
    await git.pull({
      fs: this.fsImpl,
      http: this.httpImpl,
      dir: this.repoDir(repo),
      ref: this.resolveBranch(repo),
      singleBranch: true,
      onAuth: () => ({ username, password }),
      author: { name: username || 'PubObs', email: username || 'pubobs@local' },
    });
  }

  async stage(repo: FolderRepo): Promise<string[]> {
    const dir = this.repoDir(repo);
    const statusMatrix = await git.statusMatrix({ fs: this.fsImpl, dir });
    const staged: string[] = [];
    for (const [filepath, head, workdir] of statusMatrix) {
      if (head !== workdir) {
        if (workdir === 0) {
          await git.remove({ fs: this.fsImpl, dir, filepath: filepath as string });
        } else {
          await git.add({ fs: this.fsImpl, dir, filepath: filepath as string });
        }
        staged.push(filepath as string);
      }
    }
    return staged;
  }

  async commit(repo: FolderRepo, message: string): Promise<string | null> {
    const dir = this.repoDir(repo);
    const statusMatrix = await git.statusMatrix({ fs: this.fsImpl, dir });
    const hasStagedChanges = statusMatrix.some(([, head, , stage]) => head !== stage);
    if (!hasStagedChanges) return null;

    const { username } = this.resolveAuth(repo);
    return git.commit({
      fs: this.fsImpl,
      dir,
      message,
      author: { name: username || 'PubObs', email: username || 'pubobs@local' },
    });
  }

  async push(repo: FolderRepo): Promise<void> {
    const { username, password } = this.resolveAuth(repo);
    await git.push({
      fs: this.fsImpl,
      http: this.httpImpl,
      dir: this.repoDir(repo),
      ref: this.resolveBranch(repo),
      onAuth: () => ({ username, password }),
    });
  }

  async testConnection(repo: FolderRepo): Promise<{ ok: boolean; message: string }> {
    const { username, password } = this.resolveAuth(repo);
    try {
      await git.getRemoteInfo2({
        http: this.httpImpl,
        url: repo.remoteUrl,
        onAuth: () => ({ username, password }),
      });
      return { ok: true, message: 'Connected' };
    } catch (e) {
      return { ok: false, message: (e as Error).message };
    }
  }
}
