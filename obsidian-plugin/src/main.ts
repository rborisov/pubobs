import { Plugin } from 'obsidian';
import { SettingsManager, PubObsSettingTab } from './settings';
import { SyncOrchestrator } from './orchestrator';

export default class PubObsPlugin extends Plugin {
  settingsManager!: SettingsManager;
  orchestrator!: SyncOrchestrator;

  async onload(): Promise<void> {
    this.settingsManager = new SettingsManager(this);
    await this.settingsManager.load();

    const vaultPath = (this.app.vault.adapter as any).basePath as string;
    this.orchestrator = new SyncOrchestrator(this.app, this.settingsManager, vaultPath);

    this.addCommand({
      id: 'sync-current-folder',
      name: 'Sync current folder',
      callback: () => { void this.orchestrator.syncCurrentFile(); },
    });

    this.addSettingTab(new PubObsSettingTab(this.app, this));

    if (this.settingsManager.settings.autoSync) {
      this.registerEvent(
        this.app.vault.on('modify', file => {
          const repo = this.orchestrator.resolveFolderForFile(file.path);
          if (repo) void this.orchestrator.syncFolder(repo);
        }),
      );
    }
  }

  async onunload(): Promise<void> {}
}
