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

        // Git credential entry for this repo. The token is held only in this
        // closure's local variables and submitted directly to the backend —
        // it is never written to this.plugin.settings / saveData, and the
        // backend never returns a saved token to re-render here.
        let credUsername = '';
        let credToken = '';
        let credGitName = '';
        let credGitEmail = '';

        containerEl.createEl('h4', { text: `Git credential — ${mapping.repoName}` });

        new Setting(containerEl)
          .setName('Username')
          .addText(text =>
            text
              .setPlaceholder('Git username')
              .onChange(v => { credUsername = v.trim(); })
          );

        new Setting(containerEl)
          .setName('Token')
          .addText(text => {
            text
              .setPlaceholder('Personal access token')
              .onChange(v => { credToken = v.trim(); });
            text.inputEl.type = 'password';
          });

        new Setting(containerEl)
          .setName('Git name (optional)')
          .addText(text =>
            text
              .setPlaceholder('Display name for commits')
              .onChange(v => { credGitName = v.trim(); })
          );

        new Setting(containerEl)
          .setName('Git email (optional)')
          .addText(text =>
            text
              .setPlaceholder('Email for commits')
              .onChange(v => { credGitEmail = v.trim(); })
          );

        new Setting(containerEl)
          .setDesc('Credentials are stored securely on the backend and never saved in Obsidian settings.')
          .addButton(btn =>
            btn
              .setButtonText('Save & verify')
              .setCta()
              .onClick(async () => {
                if (!credUsername || !credToken) {
                  new Notice('Enter a username and token first');
                  return;
                }
                try {
                  await this.plugin.client.setGitCredential(repoId, {
                    username: credUsername,
                    token: credToken,
                    gitName: credGitName || undefined,
                    gitEmail: credGitEmail || undefined,
                  });
                  const result = await this.plugin.client.verifyGitCredential(repoId);
                  if (result.status === 'verified') {
                    new Notice('Git credential saved — read access verified');
                  } else if (result.status === 'auth_failed') {
                    new Notice('Git credential saved — authentication failed');
                  } else {
                    new Notice(`Git credential saved — status: ${result.status}`);
                  }
                } catch (e: unknown) {
                  new Notice('Failed: ' + (e instanceof Error ? e.message : String(e)));
                }
              })
          )
          .addButton(btn =>
            btn
              .setButtonText('Remove credential')
              .setWarning()
              .onClick(async () => {
                try {
                  await this.plugin.client.deleteGitCredential(repoId);
                  new Notice('Git credential removed');
                } catch (e: unknown) {
                  new Notice('Failed: ' + (e instanceof Error ? e.message : String(e)));
                }
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
