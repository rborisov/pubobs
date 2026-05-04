import { App, PluginSettingTab, Setting, Notice } from 'obsidian';
import type PubObsPlugin from './main';
import type { RepoInfo } from './types';

export class PubObsSettingTab extends PluginSettingTab {
  constructor(app: App, private plugin: PubObsPlugin) {
    super(app, plugin);
  }

  display(): void {
    const { containerEl } = this;
    containerEl.empty();

    new Setting(containerEl)
      .setName('Backend URL')
      .setDesc('PubObs server address, e.g. https://pubobs.example.com')
      .addText(text =>
        text
          .setPlaceholder('https://pubobs.example.com')
          .setValue(this.plugin.settings.backendUrl)
          .onChange(async v => {
            this.plugin.settings.backendUrl = v.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName('Authentication')
      .setDesc(this.plugin.settings.accessToken ? 'Authenticated ✓' : 'Not authenticated')
      .addButton(btn =>
        btn
          .setButtonText('Sign in')
          .setCta()
          .onClick(async () => {
            if (!this.plugin.settings.backendUrl) {
              new Notice('Set Backend URL first');
              return;
            }
            await this.plugin.authFlow.beginAuth();
          })
      );

    if (Object.keys(this.plugin.settings.repoMappings).length > 0) {
      containerEl.createEl('h3', { text: 'Repo mappings' });

      for (const [repoId, mapping] of Object.entries(this.plugin.settings.repoMappings)) {
        new Setting(containerEl)
          .setName(mapping.repoName)
          .setDesc(`Repo ID: ${repoId}`)
          .addText(text =>
            text
              .setPlaceholder('Vault folder (e.g. Notes/Published)')
              .setValue(mapping.vaultFolder)
              .onChange(async v => {
                this.plugin.settings.repoMappings[repoId].vaultFolder = v.trim();
                await this.plugin.saveSettings();
                await this.plugin.client
                  .upsertFolderMapping(repoId, v.trim(), mapping.subfolder)
                  .catch(() => {});
              })
          );
      }
    }

    new Setting(containerEl)
      .setName('Refresh repo list')
      .setDesc('Fetch accessible repos from the backend and update mappings')
      .addButton(btn =>
        btn.setButtonText('Refresh').onClick(async () => {
          try {
            await this.plugin.refreshRepoList();
            this.display();
          } catch (e: unknown) {
            new Notice('Failed: ' + (e instanceof Error ? e.message : String(e)));
          }
        })
      );
  }
}
