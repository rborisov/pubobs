export interface FolderRepo {
  folderPath: string;
  remoteUrl: string;
  username?: string;
  pat?: string;
  branch?: string;
}

export interface PubObsSettings {
  defaultUsername: string;
  defaultPat: string;
  defaultBranch: string;
  autoSync: boolean;
  repos: FolderRepo[];
}

export const DEFAULT_SETTINGS: PubObsSettings = {
  defaultUsername: '',
  defaultPat: '',
  defaultBranch: 'main',
  autoSync: false,
  repos: [],
};

import { Plugin, App, PluginSettingTab, Setting, Notice } from 'obsidian';

interface PluginRef {
  settingsManager: SettingsManager;
  orchestrator: {
    cloneFolder(repo: FolderRepo): Promise<void>;
    testFolderConnection(repo: FolderRepo): Promise<{ ok: boolean; message: string }>;
  };
}

export class PubObsSettingTab extends PluginSettingTab {
  constructor(app: App, private plugin: PluginRef & any) {
    super(app, plugin);
  }

  display(): void {
    const { containerEl } = this;
    containerEl.empty();

    containerEl.createEl('h2', { text: 'PubObs' });

    containerEl.createEl('h3', { text: 'Default Credentials' });

    new Setting(containerEl)
      .setName('Username')
      .addText(text => text
        .setValue(this.plugin.settingsManager.settings.defaultUsername)
        .onChange(async v => this.plugin.settingsManager.update({ defaultUsername: v })));

    new Setting(containerEl)
      .setName('Personal Access Token')
      .addText(text => {
        text.inputEl.type = 'password';
        text.setValue(this.plugin.settingsManager.settings.defaultPat)
          .onChange(async v => this.plugin.settingsManager.update({ defaultPat: v }));
      });

    new Setting(containerEl)
      .setName('Default branch')
      .addText(text => text
        .setValue(this.plugin.settingsManager.settings.defaultBranch)
        .onChange(async v => this.plugin.settingsManager.update({ defaultBranch: v })));

    containerEl.createEl('h3', { text: 'Linked Folders' });

    const repos = this.plugin.settingsManager.settings.repos;
    if (repos.length === 0) {
      containerEl.createEl('p', {
        text: 'No folders linked yet.',
        cls: 'setting-item-description',
      });
    }
    for (const repo of repos) {
      this.renderRepoRow(containerEl, repo);
    }

    containerEl.createEl('h4', { text: 'Add folder' });
    let newFolderPath = '';
    let newRemoteUrl = '';

    new Setting(containerEl)
      .setName('Folder path')
      .setDesc('Relative to vault root, e.g. project-a')
      .addText(text => text
        .setPlaceholder('project-a')
        .onChange(v => { newFolderPath = v; }));

    new Setting(containerEl)
      .setName('Remote URL')
      .addText(text => text
        .setPlaceholder('https://gogs.example.com/team/project-a.git')
        .onChange(v => { newRemoteUrl = v; }));

    new Setting(containerEl)
      .setName('Clone remote into folder')
      .addButton(btn => btn
        .setButtonText('Clone / Initialize')
        .onClick(async () => {
          if (!newFolderPath || !newRemoteUrl) return;
          const repo: FolderRepo = { folderPath: newFolderPath, remoteUrl: newRemoteUrl };
          try {
            await this.plugin.orchestrator.cloneFolder(repo);
            const repos = [...this.plugin.settingsManager.settings.repos, repo];
            await this.plugin.settingsManager.update({ repos });
            this.display();
          } catch (e) {
            new Notice(`PubObs: Clone failed — ${(e as Error).message}`, 8000);
          }
        }));

    containerEl.createEl('h3', { text: 'Sync' });

    new Setting(containerEl)
      .setName('Auto-sync on save')
      .setDesc('Commit and push every time a note in a linked folder is saved (requires plugin reload)')
      .addToggle(toggle => toggle
        .setValue(this.plugin.settingsManager.settings.autoSync)
        .onChange(async v => this.plugin.settingsManager.update({ autoSync: v })));
  }

  private renderRepoRow(containerEl: HTMLElement, repo: FolderRepo): void {
    new Setting(containerEl)
      .setName(repo.folderPath)
      .setDesc(repo.remoteUrl)
      .addButton(btn => btn
        .setButtonText('Test connection')
        .onClick(async () => {
          const result = await this.plugin.orchestrator.testFolderConnection(repo);
          new Notice(
            result.ok
              ? `PubObs: ${repo.folderPath} — Connected ✓`
              : `PubObs: ${repo.folderPath} — Failed ✗ ${result.message}`,
            5000,
          );
        }))
      .addButton(btn => btn
        .setButtonText('Remove')
        .onClick(async () => {
          const repos = this.plugin.settingsManager.settings.repos
            .filter((r: FolderRepo) => r.folderPath !== repo.folderPath);
          await this.plugin.settingsManager.update({ repos });
          this.display();
        }));
  }
}

export class SettingsManager {
  settings: PubObsSettings = { ...DEFAULT_SETTINGS, repos: [] };

  constructor(private plugin: Plugin) {}

  async load(): Promise<void> {
    const saved = await this.plugin.loadData();
    Object.assign(this.settings, DEFAULT_SETTINGS, saved ?? {});
  }

  async save(): Promise<void> {
    await this.plugin.saveData(this.settings);
  }

  async update(changes: Partial<PubObsSettings>): Promise<void> {
    Object.assign(this.settings, changes);
    await this.save();
  }
}
