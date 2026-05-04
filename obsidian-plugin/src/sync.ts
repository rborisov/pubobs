import { App, TFile, Notice } from 'obsidian';
import type { BackendClient, SyncFile, SyncAsset } from './client';
import type { PubObsSettings } from './types';
import { renderNoteToHTML, extractStyles } from './renderer';

export class SyncManager {
  constructor(
    private app: App,
    private client: BackendClient,
    private settings: PubObsSettings,
    private saveSettings: () => Promise<void>,
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
    const notePlugins: Record<string, string[]> = {};

    for (let i = 0; i < files.length; i++) {
      const f = files[i];
      notice.setMessage(`Rendering ${i + 1} / ${files.length}: ${f.basename}…`);
      try {
        const content = await this.app.vault.read(f);
        const used = detectPlugins(content);
        const { syncFile, assets } = await this.buildSyncFile(f, vaultFolder, subfolder, repoId);
        syncFiles.push(syncFile);
        if (used.length > 0) notePlugins[syncFile.path] = used;
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

    // Record per-note plugin requirements so pull-in can warn about missing plugins
    syncAssets.push({
      path: '_pubobs/note-plugins.json',
      content: bufferToBase64(new TextEncoder().encode(notePluginsJson(notePlugins)).buffer),
    });

    console.log(`[PubObs] syncing ${syncFiles.length} notes, ${syncAssets.length} assets`);
    const result = await this.client.sync(repoId, syncFiles, syncAssets);
    new Notice(`Synced ${syncFiles.length} note(s), ${syncAssets.length} asset(s) — ${result.commit_sha.slice(0, 7)}`);
  }

  async pullRepo(repoId: string): Promise<void> {
    const mapping = this.settings.repoMappings[repoId];
    if (!mapping) throw new Error(`No folder mapping for repo ${repoId}`);

    const { vaultFolder, subfolder } = mapping;
    const files = await this.client.listFiles(repoId);

    const notePluginsFile = files.find(f => f.path === '_pubobs/note-plugins.json');

    // Only pull .md notes; skip rendered assets like _pubobs/obsidian.css
    const noteFiles = files.filter(f => f.path.endsWith('.md') && !f.path.startsWith('_pubobs/'));

    const storedSHAs: Record<string, string> = { ...(this.settings.pullSHAs[repoId] ?? {}) };
    let pulled = 0;
    let skipped = 0;
    const pulledPaths: string[] = [];

    for (const file of noteFiles) {
      if (storedSHAs[file.path] === file.sha) {
        skipped++;
        continue;
      }

      const vaultPath = repoPathToVaultPath(file.path, vaultFolder, subfolder);

      // Ensure parent directory exists
      const dir = vaultPath.split('/').slice(0, -1).join('/');
      if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
        await this.app.vault.createFolder(dir);
      }

      const existing = this.app.vault.getAbstractFileByPath(vaultPath);
      if (existing instanceof TFile) {
        await this.app.vault.modify(existing, file.content);
      } else {
        await this.app.vault.create(vaultPath, file.content);
      }

      storedSHAs[file.path] = file.sha;
      pulledPaths.push(file.path);
      pulled++;
    }

    this.settings.pullSHAs[repoId] = storedSHAs;
    await this.saveSettings();

    new Notice(`PubObs: pulled ${pulled} file(s), ${skipped} unchanged`);

    // Warn about missing plugins only for notes that were actually pulled
    if (notePluginsFile && pulledPaths.length > 0) {
      warnMissingPlugins(this.app, notePluginsFile.content, pulledPaths);
    }
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

// Registry of known plugins and the markdown patterns that indicate their usage.
// Each entry maps a community plugin ID to one or more patterns found in note content.
const PLUGIN_PATTERNS: Array<{ id: string; name: string; patterns: RegExp[] }> = [
  { id: 'dataview',                    name: 'Dataview',         patterns: [/^```dataviewjs?\b/m, /`=[^`]+`/] },
  { id: 'obsidian-excalidraw-plugin',  name: 'Excalidraw',       patterns: [/!\[\[.*\.excalidraw(\|[^\]]*)?\]\]/] },
  { id: 'obsidian-kanban',             name: 'Kanban',           patterns: [/%% kanban:settings/] },
  { id: 'templater-obsidian',          name: 'Templater',        patterns: [/<%[-_]?\s/] },
  { id: 'obsidian-tasks-plugin',       name: 'Tasks',            patterns: [/^```tasks\b/m] },
  { id: 'obsidian-charts',             name: 'Charts',           patterns: [/^```chart\b/m] },
  { id: 'obsidian-admonition',         name: 'Admonition',       patterns: [/^```ad-\w/m] },
  { id: 'obsidian-map-view',           name: 'Map View',         patterns: [/^```mapview\b/m] },
  { id: 'obsidian-leaflet-plugin',     name: 'Leaflet',          patterns: [/^```leaflet\b/m] },
  { id: 'table-editor-obsidian',       name: 'Advanced Tables',  patterns: [/^\|.+\|\s*$\n^\|\s*[-:]+[-| :]*\|\s*$/m] },
  { id: 'obsidian-outliner',           name: 'Outliner',         patterns: [] }, // no detectable syntax
];

function detectPlugins(content: string): string[] {
  return PLUGIN_PATTERNS
    .filter(p => p.patterns.length > 0 && p.patterns.some(re => re.test(content)))
    .map(p => p.id);
}

function notePluginsJson(notePlugins: Record<string, string[]>): string {
  return JSON.stringify(notePlugins, null, 2);
}

function warnMissingPlugins(app: App, notePluginsJson: string, pulledPaths: string[]): void {
  let notePlugins: Record<string, string[]>;
  try { notePlugins = JSON.parse(notePluginsJson); } catch { return; }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const installed: Set<string> = (app as any).plugins?.enabledPlugins ?? new Set();
  const missingIds = new Set<string>();
  for (const path of pulledPaths) {
    for (const id of notePlugins[path] ?? []) {
      if (!installed.has(id)) missingIds.add(id);
    }
  }

  if (missingIds.size > 0) {
    const names = Array.from(missingIds).map(id => {
      const entry = PLUGIN_PATTERNS.find(p => p.id === id);
      return entry ? `• ${entry.name} (${id})` : `• ${id}`;
    });
    new Notice(
      `PubObs: pulled notes require plugin(s) not installed:\n${names.join('\n')}`,
      8000,
    );
  }
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
