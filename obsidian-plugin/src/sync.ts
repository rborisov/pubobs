import { App, TFile, Notice } from 'obsidian';
import type { BackendClient, SyncFile, SyncAsset } from './client';
import type { PubObsSettings } from './types';
import { renderNoteToHTML, extractStyles } from './renderer';

export class SyncManager {
  constructor(
    private app: App,
    private client: BackendClient,
    private settings: PubObsSettings,
  ) {}

  async syncRepo(repoId: string): Promise<void> {
    const mapping = this.settings.repoMappings[repoId];
    if (!mapping) throw new Error(`No folder mapping for repo ${repoId}`);

    const { vaultFolder, subfolder } = mapping;
    const files = this.app.vault
      .getFiles()
      .filter((f: TFile) => f.extension === 'md' && (vaultFolder === '' || f.path.startsWith(vaultFolder + '/')));

    const notice = new Notice(`Rendering 0 / ${files.length}…`, 0);
    const syncFiles: SyncFile[] = [];
    const assetMap = new Map<string, ArrayBuffer>(); // vault path → binary (deduplicated)

    for (let i = 0; i < files.length; i++) {
      const f = files[i];
      notice.setMessage(`Rendering ${i + 1} / ${files.length}: ${f.basename}…`);
      try {
        const { syncFile, assets } = await this.buildSyncFile(f, vaultFolder, subfolder, repoId);
        syncFiles.push(syncFile);
        for (const [vaultPath, buf] of assets) {
          assetMap.set(vaultPath, buf);
        }
      } catch (e) {
        console.error(`[PubObs] render failed for ${f.path}: ${e}`);
      }
    }
    notice.hide();

    const syncAssets: SyncAsset[] = Array.from(assetMap.entries()).map(([vaultPath, buf]) => ({
      path: this.assetRepoPath(vaultPath, vaultFolder, subfolder),
      content: bufferToBase64(buf),
    }));

    // Capture current Obsidian CSS (theme + plugins) so the reader renders identically
    const css = extractStyles();
    syncAssets.push({
      path: '_pubobs/obsidian.css',
      content: bufferToBase64(new TextEncoder().encode(css).buffer),
    });

    console.log(`[PubObs] syncing ${syncFiles.length} notes, ${syncAssets.length} assets`);
    const result = await this.client.sync(repoId, syncFiles, syncAssets);
    new Notice(`Synced ${syncFiles.length} note(s), ${syncAssets.length} asset(s) — ${result.commit_sha.slice(0, 7)}`);
  }

  private async buildSyncFile(
    file: TFile,
    vaultFolder: string,
    subfolder: string,
    repoId: string,
  ): Promise<{ syncFile: SyncFile; assets: Map<string, ArrayBuffer> }> {
    const content = await this.app.vault.read(file);
    const cache = this.app.metadataCache.getFileCache(file);
    const { position: _pos, ...frontmatter } = ((cache?.frontmatter ?? {}) as Record<string, unknown>);

    let relative = file.path;
    if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
      relative = relative.slice(vaultFolder.length + 1);
    }
    const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;

    const { html, assets } = await renderNoteToHTML(this.app, content, file.path, repoId, vaultFolder, subfolder);

    return {
      syncFile: { path: repoPath, md_content: content, html_content: html, frontmatter },
      assets,
    };
  }

  private assetRepoPath(vaultPath: string, vaultFolder: string, subfolder: string): string {
    let p = vaultPath;
    if (vaultFolder && p.startsWith(vaultFolder + '/')) {
      p = p.slice(vaultFolder.length + 1);
    }
    if (subfolder) {
      p = `${subfolder.replace(/\/$/, '')}/${p}`;
    }
    return p;
  }
}

function bufferToBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let binary = '';
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary);
}

/**
 * Maps a repo-relative file path back to a vault path.
 * Inverse of the sync-out transform: vaultPath → repoPath.
 *
 * sync-out: strip vaultFolder prefix, add subfolder prefix
 * sync-in:  strip subfolder prefix, add vaultFolder prefix
 */
export function repoPathToVaultPath(repoPath: string, vaultFolder: string, subfolder: string): string {
  let p = repoPath;
  const sub = subfolder.replace(/\/$/, '');
  if (sub && p.startsWith(sub + '/')) {
    p = p.slice(sub.length + 1);
  }
  return vaultFolder ? `${vaultFolder}/${p}` : p;
}
