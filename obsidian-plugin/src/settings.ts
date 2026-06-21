import { App, PluginSettingTab, Setting, Notice, AbstractInputSuggest, TFolder } from 'obsidian';
import type PubObsPlugin from './main';
import type { RepoInfo } from './types';

class FolderSuggest extends AbstractInputSuggest<TFolder> {
  constructor(app: App, inputEl: HTMLInputElement) {
    super(app, inputEl);
  }

  getSuggestions(query: string): TFolder[] {
    const lq = query.toLowerCase();
    return this.app.vault.getAllLoadedFiles()
      .filter((f): f is TFolder => f instanceof TFolder && f.path !== '/' && f.path.toLowerCase().includes(lq))
      .sort((a, b) => a.path.localeCompare(b.path));
  }

  renderSuggestion(folder: TFolder, el: HTMLElement): void {
    el.setText(folder.path);
  }

  selectSuggestion(folder: TFolder): void {
    this.setValue(folder.path);
    this.close();
  }
}

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
        const save = async (value: string) => {
          this.plugin.settings.repoMappings[repoId].vaultFolder = value;
          await this.plugin.saveSettings();
          await this.plugin.client
            .upsertFolderMapping(repoId, value, mapping.subfolder)
            .catch(() => {});
        };

        new Setting(containerEl)
          .setName(mapping.repoName)
          .setDesc(`Repo ID: ${repoId}`)
          .addText(text => {
            text
              .setPlaceholder('Select vault folder…')
              .setValue(mapping.vaultFolder)
              .onChange(async v => save(v.trim()));

            const suggest = new FolderSuggest(this.app, text.inputEl);
            suggest.onSelect(async (folder) => save(folder.path));
          });
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
