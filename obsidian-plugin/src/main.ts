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
        const repoIds = Object.keys(this.settings.repoMappings);
        if (repoIds.length === 0) {
          new Notice('PubObs: no repos configured — open Settings to add one');
          return;
        }
        for (const id of repoIds) {
          try {
            await this.syncManager.syncRepo(id);
          } catch (e: unknown) {
            new Notice(`PubObs sync failed (${id}): ` + (e instanceof Error ? e.message : String(e)));
          }
        }
      },
    });

    this.addCommand({
      id: 'pull-all',
      name: 'Pull all repos',
      callback: async () => {
        const repoIds = Object.keys(this.settings.repoMappings);
        if (repoIds.length === 0) {
          new Notice('PubObs: no repos configured — open Settings to add one');
          return;
        }
        for (const id of repoIds) {
          try {
            await this.syncManager.pullRepo(id);
          } catch (e: unknown) {
            new Notice(`PubObs pull failed (${id}): ` + (e instanceof Error ? e.message : String(e)));
          }
        }
      },
    });

    this.settingTab = new PubObsSettingTab(this.app, this);
    this.addSettingTab(this.settingTab);
  }

  async loadSettings(): Promise<void> {
    this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
  }

  async refreshRepoList(): Promise<void> {
    const repos: RepoInfo[] = await this.client.listRepos();
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
