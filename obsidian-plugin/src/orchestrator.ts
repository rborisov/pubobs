import { App, Notice } from 'obsidian';
import { SettingsManager, FolderRepo } from './settings';
import { EnvironmentValidator } from './validator';
import { GitService } from './git';

export class SyncOrchestrator {
  private validator: EnvironmentValidator;
  private git: GitService;

  constructor(
    private app: App,
    private settingsManager: SettingsManager,
    vaultPath: string,
  ) {
    this.validator = new EnvironmentValidator(app);
    this.git = new GitService(vaultPath, () => settingsManager.settings);
  }

  resolveFolderForFile(filePath: string): FolderRepo | undefined {
    return this.settingsManager.settings.repos.find(
      repo => filePath === repo.folderPath || filePath.startsWith(repo.folderPath + '/'),
    );
  }

  async cloneFolder(repo: FolderRepo): Promise<void> {
    await this.git.clone(repo);
  }

  async testFolderConnection(repo: FolderRepo): Promise<{ ok: boolean; message: string }> {
    return this.git.testConnection(repo);
  }

  async syncCurrentFile(): Promise<void> {
    const activeFile = this.app.workspace.getActiveFile();
    if (!activeFile) {
      new Notice('PubObs: No active file.');
      return;
    }
    const repo = this.resolveFolderForFile(activeFile.path);
    if (!repo) {
      new Notice('PubObs: This file is not in a linked PubObs folder.');
      return;
    }
    await this.syncFolder(repo);
  }

  async syncFolder(repo: FolderRepo): Promise<void> {
    const validation = await this.validator.check(repo.folderPath);
    if (!validation.valid) {
      for (const error of validation.errors) {
        new Notice(error.message, 8000);
      }
      return;
    }

    try {
      new Notice(`PubObs: Syncing ${repo.folderPath}…`);
      if (await this.git.hasCommits(repo)) {
        await this.git.pull(repo);
      }
      const staged = await this.git.stage(repo);
      const timestamp = new Date().toISOString();
      const sha = await this.git.commit(repo, `pubobs: sync ${timestamp}`);
      await this.git.push(repo);
      if (!sha && staged.length === 0) {
        new Notice('PubObs: Nothing to sync.');
        return;
      }
      new Notice(`PubObs: Synced ${staged.length} file(s)${sha ? ` — ${sha.slice(0, 7)}` : ''}`);
    } catch (e) {
      const msg = (e as Error).message ?? String(e);
      if (/401|403|auth/i.test(msg)) {
        new Notice(
          'PubObs: Push failed — authentication error. Check your token in PubObs settings.',
          10000,
        );
      } else {
        new Notice(`PubObs: Sync failed — ${msg}`, 8000);
      }
    }
  }
}
