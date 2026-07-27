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
      .setName('Data file types')
      .setDesc('Comma-separated extensions synced both ways alongside notes (Bases, CSV, JSON, YAML). Leave empty to sync notes only.')
      .addText(text =>
        text
          .setPlaceholder('base, csv, json, yaml, yml')
          .setValue(this.plugin.settings.dataFileExtensions)
          .onChange(async v => {
            this.plugin.settings.dataFileExtensions = v;
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName('Data file size limit (MB)')
      .setDesc('Data files larger than this are skipped in both directions and named in the sync report. The server caps this at 25 MB.')
      .addText(text =>
        text
          .setPlaceholder('5')
          .setValue(String(this.plugin.settings.dataFileMaxMB))
          .onChange(async v => {
            const n = Number(v.trim());
            this.plugin.settings.dataFileMaxMB = Number.isFinite(n) && n > 0 ? n : 5;
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

      // Git credentials are grouped by git provider (host), not by repo:
      // one token typically covers every repo on the same host. Rendered
      // asynchronously because it needs each repo's remote URL from the
      // backend. Tokens live only in the render closure — never in
      // this.plugin.settings / saveData, and the backend never returns a
      // saved token.
      const gitCredsEl = containerEl.createDiv();
      void this.renderGitCredentials(gitCredsEl);
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

  // Render one credential entry per git provider (host), covering every
  // mapped repo on that host. Async because it needs each repo's remote URL.
  private async renderGitCredentials(el: HTMLElement): Promise<void> {
    el.empty();
    const mappings = this.plugin.settings.repoMappings;
    const mappedIds = Object.keys(mappings);
    if (mappedIds.length === 0) return;

    let repos: RepoInfo[];
    try {
      repos = await this.plugin.client.listRepos();
    } catch {
      return; // offline / not authenticated — skip silently
    }

    const mapped = new Set(mappedIds);
    const byHost = new Map<string, { repoId: string; repoName: string }[]>();
    for (const r of repos) {
      if (!mapped.has(r.id)) continue;
      const host = hostOf(r.remote_url);
      const list = byHost.get(host) ?? [];
      list.push({ repoId: r.id, repoName: mappings[r.id].repoName });
      byHost.set(host, list);
    }
    if (byHost.size === 0) return;

    el.createEl('h3', { text: 'Git credentials' });
    el.createEl('p', {
      text: 'One credential per git provider — it applies to every mapped repo on that host.',
      cls: 'setting-item-description',
    });
    for (const [host, entries] of byHost) {
      this.renderHostCredential(el, host, entries);
    }
  }

  private renderHostCredential(
    el: HTMLElement, host: string, entries: { repoId: string; repoName: string }[],
  ): void {
    let username = '';
    let token = '';
    const repoList = entries.map(e => e.repoName).join(', ');

    el.createEl('h4', { text: `Git credential — ${host}` });
    const statusEl = el.createEl('p', { text: 'Checking…', cls: 'setting-item-description' });

    const renderStatus = (statuses: { configured: boolean; verify_status: string }[]) => {
      const configured = statuses.filter(s => s.configured).length;
      const verified = statuses.filter(s => s.verify_status === 'verified').length;
      if (configured === 0) {
        statusEl.setText(`Not set · applies to: ${repoList}`);
      } else {
        statusEl.setText(
          `Credential set for ${configured}/${entries.length} repo(s) · ` +
          `write-verified ${verified}/${entries.length} · applies to: ${repoList}`,
        );
      }
    };
    void Promise.all(
      entries.map(e =>
        this.plugin.client
          .getGitCredentialStatus(e.repoId)
          .catch(() => ({ configured: false, verify_status: '' })),
      ),
    ).then(renderStatus);

    new Setting(el)
      .setName('Username')
      .addText(text =>
        text.setPlaceholder('Git username').onChange(v => { username = v.trim(); }),
      );

    new Setting(el)
      .setName('Token')
      .addText(text => {
        text.setPlaceholder('Personal access token').onChange(v => { token = v.trim(); });
        text.inputEl.type = 'password';
      });

    new Setting(el)
      .setDesc('Stored securely on the backend and never saved in Obsidian settings.')
      .addButton(btn =>
        btn
          .setButtonText('Save & verify')
          .setCta()
          .onClick(async () => {
            if (!username || !token) {
              new Notice('Enter a username and token first');
              return;
            }
            try {
              let verified = 0;
              const failed: string[] = [];
              for (const e of entries) {
                await this.plugin.client.setGitCredential(e.repoId, { username, token });
                const result = await this.plugin.client.verifyGitCredential(e.repoId);
                if (result.status === 'verified') verified++;
                else failed.push(e.repoName);
              }
              if (failed.length === 0) {
                new Notice(`Credential saved — write access verified for all ${entries.length} repo(s) on ${host}`);
              } else {
                new Notice(`Credential saved — write access failed for: ${failed.join(', ')}`);
              }
              renderStatus(
                entries.map((_, i) => ({
                  configured: true,
                  verify_status: i < verified ? 'verified' : 'auth_failed',
                })),
              );
            } catch (e: unknown) {
              new Notice('Failed: ' + (e instanceof Error ? e.message : String(e)));
            }
          }),
      )
      .addButton(btn =>
        btn
          .setButtonText('Remove')
          .setWarning()
          .onClick(async () => {
            try {
              for (const e of entries) {
                await this.plugin.client.deleteGitCredential(e.repoId);
              }
              new Notice(`Credential removed for ${host}`);
              renderStatus(entries.map(() => ({ configured: false, verify_status: '' })));
            } catch (e: unknown) {
              new Notice('Failed: ' + (e instanceof Error ? e.message : String(e)));
            }
          }),
      );
  }
}

// hostOf extracts the provider host from a git remote URL. Handles both
// https (`https://host/path`) and scp-style ssh (`git@host:path`) remotes,
// falling back to the raw URL if it can't be parsed so distinct remotes are
// never merged under one blank key.
function hostOf(remoteUrl: string): string {
  try {
    return new URL(remoteUrl).host;
  } catch {
    const m = remoteUrl.match(/@([^:/]+)[:/]/);
    return m ? m[1] : remoteUrl;
  }
}
