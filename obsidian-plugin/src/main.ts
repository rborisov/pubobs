import { Plugin, Notice } from 'obsidian';
import { BackendClient } from './client';
import { AuthFlow } from './auth';
import { SyncManager } from './sync';
import { PubObsSettingTab } from './settings';
import { DEFAULT_SETTINGS } from './types';
import type { PubObsSettings, RepoInfo } from './types';

export default class PubObsPlugin extends Plugin {
  settings!: PubObsSettings;
  client!: BackendClient;
  authFlow!: AuthFlow;
  syncManager!: SyncManager;
  private settingTab!: PubObsSettingTab;

  async onload(): Promise<void> {
    await this.loadSettings();

    this.client = new BackendClient(this.settings, () => this.saveSettings());
    this.authFlow = new AuthFlow(this.client, () => this.settings.backendUrl);
    this.syncManager = new SyncManager(this.app, this.client, this.settings, () => this.saveSettings());

    this.registerObsidianProtocolHandler('pubobs-callback', async params => {
      await this.authFlow.handleCallback(
        params,
        async () => {
          await this.saveSettings();
          new Notice('PubObs: signed in successfully');
          await this.refreshRepoList();
          this.settingTab.display();
        },
        msg => new Notice(`PubObs auth error: ${msg}`),
      );
    });

    this.addCommand({
      id: 'sync-all',
      name: 'Sync all repos',
      callback: async () => {
        await this.syncAllRepos();
      },
    });

    this.addCommand({
      id: 'force-resync-all',
      name: 'Force re-sync all notes',
      callback: async () => {
        await this.syncAllRepos({ force: true });
      },
    });

    this.addCommand({
      id: 'copy-share-link',
      name: 'Copy share link for this note',
      // checkCallback lets Obsidian hide/disable this command from the
      // palette entirely when the active file isn't a currently-synced note
      // in a currently-configured repo, rather than showing it and failing.
      checkCallback: (checking: boolean) => {
        const target = this.resolveActiveNoteTarget();
        if (!target) return false;
        if (!checking) void this.copyShareLinkForNote(target.repoId, target.repoPath);
        return true;
      },
    });

    this.settingTab = new PubObsSettingTab(this.app, this);
    this.addSettingTab(this.settingTab);
  }

  private async syncAllRepos(opts?: { force?: boolean }): Promise<void> {
    const repoIds = Object.keys(this.settings.repoMappings);
    if (repoIds.length === 0) {
      new Notice('PubObs: no repos configured — open Settings to add one');
      return;
    }
    for (const id of repoIds) {
      try {
        await this.syncManager.syncRepo(id, opts);
      } catch (e: unknown) {
        new Notice(`PubObs sync failed (${id}): ` + (e instanceof Error ? e.message : String(e)));
      }
    }
  }

  async loadSettings(): Promise<void> {
    this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
  }

  // resolveActiveNoteTarget determines whether the active file maps to a
  // currently-synced note in a currently-configured repo, mirroring the same
  // vaultFolder/subfolder → repoPath transform SyncManager uses, and the same
  // "vaultFolder must be set" precondition SyncManager.syncRepo enforces
  // before it will sync a repo at all. Returns null when the active file
  // isn't under any configured repo mapping, or hasn't been synced yet
  // (no stored hash for it) — the /share endpoint needs the note to already
  // exist server-side.
  private resolveActiveNoteTarget(): { repoId: string; repoPath: string } | null {
    const file = this.app.workspace.getActiveFile();
    if (!file || file.extension !== 'md') return null;

    for (const [repoId, mapping] of Object.entries(this.settings.repoMappings)) {
      const { vaultFolder, subfolder } = mapping;
      if (!vaultFolder || !file.path.startsWith(vaultFolder + '/')) continue;
      const relative = file.path.slice(vaultFolder.length + 1);
      const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;
      if (!this.settings.syncHashes[repoId]?.[repoPath]) continue;
      return { repoId, repoPath };
    }
    return null;
  }

  private async copyShareLinkForNote(repoId: string, repoPath: string): Promise<void> {
    try {
      await this.client.shareNote(repoId, repoPath, 'restricted');
      const base = this.settings.backendUrl.replace(/\/$/, '');
      const url = `${base}/#/read/${repoId}/${repoPath}`;
      await navigator.clipboard.writeText(url);
      new Notice('PubObs: share link copied to clipboard');
    } catch (e: unknown) {
      new Notice(`PubObs: failed to copy share link — ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async refreshRepoList(): Promise<void> {
    const repos: RepoInfo[] = await this.client.listRepos();
    const liveIds = new Set(repos.map(r => r.id));

    // Remove mappings for repos that no longer exist in the backend
    for (const id of Object.keys(this.settings.repoMappings)) {
      if (!liveIds.has(id)) delete this.settings.repoMappings[id];
    }

    for (const repo of repos) {
      if (!this.settings.repoMappings[repo.id]) {
        this.settings.repoMappings[repo.id] = {
          repoName: repo.name,
          vaultFolder: '',
          subfolder: '',
        };
      } else {
        this.settings.repoMappings[repo.id].repoName = repo.name;
      }
    }
    await this.saveSettings();
  }
}
