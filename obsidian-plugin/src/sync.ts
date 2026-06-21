import { App, TFile, Notice, Modal, parseYaml, stringifyYaml } from 'obsidian';
import type { BackendClient, SyncFile, SyncAsset } from './client';
import type { PubObsSettings } from './types';
import { renderNoteToHTML, extractStyles } from './renderer';

// ── Exported helpers ─────────────────────────────────────────────────────────

export function semverGte(installed: string, required: string): boolean {
  const parse = (v: string) => v.split('.').map(n => parseInt(n, 10) || 0);
  const [iMaj, iMin, iPat] = parse(installed);
  const [rMaj, rMin, rPat] = parse(required);
  if (iMaj !== rMaj) return iMaj > rMaj;
  if (iMin !== rMin) return iMin > rMin;
  return iPat >= rPat;
}

export function injectPluginFrontmatter(
  content: string,
  plugins: Array<{ id: string; version: string }>,
): string {
  const fmMatch = content.match(/^---\n([\s\S]*?)\n---\n?/);
  if (fmMatch) {
    let fm: Record<string, unknown>;
    try { fm = (parseYaml(fmMatch[1]) as Record<string, unknown>) ?? {}; }
    catch { fm = {}; }

    if (plugins.length > 0) {
      fm['pubobs-plugins'] = plugins;
    } else {
      delete fm['pubobs-plugins'];
      if (!fmMatch[1].includes('pubobs-plugins')) return content;
    }
    const fmStr = stringifyYaml(fm);
    return `---\n${fmStr}---\n${content.slice(fmMatch[0].length)}`;
  }
  if (plugins.length === 0) return content;
  const fmStr = stringifyYaml({ 'pubobs-plugins': plugins });
  return `---\n${fmStr}---\n${content}`;
}

export function parseFrontmatterPlugins(content: string): Array<{ id: string; version: string }> {
  const fmMatch = content.match(/^---\n([\s\S]*?)\n---/);
  if (!fmMatch) return [];
  try {
    const fm = parseYaml(fmMatch[1]) as Record<string, unknown>;
    const plugins = fm['pubobs-plugins'];
    if (!Array.isArray(plugins)) return [];
    return plugins.filter(
      (p): p is { id: string; version: string } =>
        typeof p === 'object' && p !== null &&
        'id' in p && typeof (p as Record<string, unknown>).id === 'string' &&
        'version' in p && typeof (p as Record<string, unknown>).version === 'string',
    );
  } catch {
    return [];
  }
}

// ── Plugin compatibility modal ────────────────────────────────────────────────

interface IncompatibleNote {
  path: string;
  missing: string[];
  content: string;
  sha: string;
}

class IncompatibleNotesModal extends Modal {
  constructor(
    app: App,
    private notes: IncompatibleNote[],
    private onCreateCopies: (notes: IncompatibleNote[]) => Promise<void>,
    private onSkip: () => void,
  ) {
    super(app);
  }

  onOpen() {
    const { contentEl } = this;
    contentEl.createEl('h2', { text: 'Plugin Compatibility' });
    contentEl.createEl('p', {
      text: `${this.notes.length} note(s) require plugin(s) that are not installed:`,
    });
    const ul = contentEl.createEl('ul');
    for (const n of this.notes) {
      ul.createEl('li', { text: `${n.path} — needs: ${n.missing.join(', ')}` });
    }
    contentEl.createEl('p', { text: 'Create local copies linked to the originals instead of pulling?' });
    const row = contentEl.createDiv({ cls: 'modal-button-container' });
    const copyBtn = row.createEl('button', { text: 'Create Copies', cls: 'mod-cta' });
    copyBtn.onclick = () => { this.close(); void this.onCreateCopies(this.notes); };
    const skipBtn = row.createEl('button', { text: 'Skip' });
    skipBtn.onclick = () => { this.close(); this.onSkip(); };
  }

  onClose() { this.contentEl.empty(); }
}

// ── Encryption helpers ────────────────────────────────────────────────────────

function base64urlEncode(data: Uint8Array): string {
  return btoa(String.fromCharCode(...data))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

function base64urlDecode(s: string): Uint8Array {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  const padded = b64 + '='.repeat((4 - b64.length % 4) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

async function encryptHTML(html: string, keyBytes: Uint8Array): Promise<string> {
  const rawKey = new Uint8Array(keyBytes).buffer as ArrayBuffer;
  const cryptoKey = await crypto.subtle.importKey('raw', rawKey, 'AES-GCM', false, ['encrypt']);
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, cryptoKey, new TextEncoder().encode(html));
  const blob = new Uint8Array(12 + ciphertext.byteLength);
  blob.set(iv, 0);
  blob.set(new Uint8Array(ciphertext), 12);
  return bufferToBase64(blob.buffer);
}

function injectFrontmatterFields(content: string, fields: Record<string, string>): string {
  const fmMatch = content.match(/^---\n([\s\S]*?)\n---\n?/);
  if (fmMatch) {
    let fm: Record<string, unknown>;
    try { fm = (parseYaml(fmMatch[1]) as Record<string, unknown>) ?? {}; }
    catch { fm = {}; }
    for (const [k, v] of Object.entries(fields)) fm[k] = v;
    const fmStr = stringifyYaml(fm);
    return `---\n${fmStr}---\n${content.slice(fmMatch[0].length)}`;
  }
  const fm: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(fields)) fm[k] = v;
  const fmStr = stringifyYaml(fm);
  return `---\n${fmStr}---\n${content}`;
}

// ── SyncManager ───────────────────────────────────────────────────────────────

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

    // ── Pull phase ──────────────────────────────────────────────────────────────
    const notice = new Notice('PubObs: checking for remote changes…', 0);
    try {
      const remoteFiles = await this.client.listFiles(repoId);
      const storedPullSHAs: Record<string, string> = { ...(this.settings.pullSHAs[repoId] ?? {}) };
      const noteFiles = remoteFiles.filter(f => f.path.endsWith('.md') && !f.path.startsWith('_pubobs/'));

      const incompatible: IncompatibleNote[] = [];
      const toPull: typeof noteFiles = [];

      for (const file of noteFiles) {
        if (storedPullSHAs[file.path] === file.sha) continue;
        const required = parseFrontmatterPlugins(file.content)
          .filter(p => PLUGIN_PATTERNS.some(pp => pp.id === p.id));
        if (required.length > 0) {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          const manifests = (this.app as any).plugins?.manifests ?? {};
          const missing = required
            .filter(p => {
              const installedVersion: string | undefined = manifests[p.id]?.version;
              return !installedVersion || !semverGte(installedVersion, p.version);
            })
            .map(p => {
              const entry = PLUGIN_PATTERNS.find(pp => pp.id === p.id);
              return entry ? `${entry.name} v${p.version}` : `${p.id} v${p.version}`;
            });
          if (missing.length > 0) {
            incompatible.push({ path: file.path, missing, content: file.content, sha: file.sha });
            continue;
          }
        }
        toPull.push(file);
      }

      let pulled = 0;
      for (const file of toPull) {
        const vaultPath = repoPathToVaultPath(file.path, vaultFolder, subfolder);
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
        storedPullSHAs[file.path] = file.sha;
        pulled++;
      }
      this.settings.pullSHAs[repoId] = storedPullSHAs;
      await this.saveSettings();

      if (incompatible.length > 0) {
        await new Promise<void>(resolve => {
          new IncompatibleNotesModal(
            this.app,
            incompatible,
            async (notes) => {
              for (const n of notes) {
                await this.createLocalCopy(vaultFolder, subfolder, n);
              }
              resolve();
            },
            resolve,
          ).open();
        });
      }

      if (pulled > 0) notice.setMessage(`PubObs: pulled ${pulled} note(s), pushing local changes…`);
    } catch (e) {
      console.error('[PubObs] pull phase failed:', e);
    }

    // ── Push phase ────────────────────────────────────────────────────────────
    const vaultFiles = this.app.vault
      .getFiles()
      .filter((f: TFile) => f.extension === 'md' && (vaultFolder === '' || f.path.startsWith(vaultFolder + '/')));

    const storedHashes: Record<string, string> = { ...(this.settings.syncHashes[repoId] ?? {}) };
    const newHashes: Record<string, string> = {};
    const currentRepoPaths = new Set<string>();

    notice.setMessage(`Checking ${vaultFiles.length} note(s)…`);
    const syncFiles: SyncFile[] = [];
    const assetMap = new Map<string, ArrayBuffer>();
    let skipped = 0;

    for (let i = 0; i < vaultFiles.length; i++) {
      const f = vaultFiles[i];
      try {
        const content = await this.app.vault.read(f);

        let relative = f.path;
        if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
          relative = relative.slice(vaultFolder.length + 1);
        }
        const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;
        currentRepoPaths.add(repoPath);

        const hash = fnv1a(content);
        newHashes[repoPath] = hash;

        if (storedHashes[repoPath] === hash) {
          skipped++;
          continue;
        }

        notice.setMessage(`Rendering ${syncFiles.length + 1}: ${f.basename}…`);
        const used = detectPlugins(content);
        const { syncFile, assets } = await this.buildSyncFile(f, content, vaultFolder, subfolder, repoId, used);
        syncFiles.push(syncFile);
        for (const [vaultPath, buf] of assets) assetMap.set(vaultPath, buf);
      } catch (e) {
        console.error(`[PubObs] render failed for ${f.path}: ${e}`);
      }
    }
    notice.hide();

    // Paths that were previously synced but no longer exist in the vault
    const deletedPaths = Object.keys(storedHashes).filter(p => !currentRepoPaths.has(p));

    // Remove render keys for notes that no longer exist
    if (this.settings.renderKeys[repoId]) {
      for (const p of deletedPaths) {
        delete this.settings.renderKeys[repoId][p];
      }
    }

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

    if (syncFiles.length === 0 && deletedPaths.length === 0) {
      new Notice(`PubObs: nothing changed (${skipped} note(s) up to date)`);
      return;
    }

    console.log(`[PubObs] syncing ${syncFiles.length} changed, ${deletedPaths.length} deleted, ${skipped} unchanged`);
    const result = await this.client.sync(repoId, syncFiles, syncAssets, deletedPaths);

    // Persist hashes only after a successful sync
    for (const p of deletedPaths) delete newHashes[p];
    this.settings.syncHashes[repoId] = newHashes;
    await this.saveSettings();

    new Notice(`PubObs: ${syncFiles.length} synced, ${deletedPaths.length} deleted, ${skipped} unchanged — ${result.commit_sha.slice(0, 7)}`);
  }

  private async createLocalCopy(
    vaultFolder: string,
    subfolder: string,
    note: IncompatibleNote,
  ): Promise<void> {
    const vaultPath = repoPathToVaultPath(note.path, vaultFolder, subfolder);
    const ext = '.md';
    const base = vaultPath.slice(0, -ext.length);

    let copyPath = `${base}-local-copy${ext}`;
    let suffix = 2;
    while (this.app.vault.getAbstractFileByPath(copyPath)) {
      copyPath = `${base}-local-copy-${suffix}${ext}`;
      suffix++;
    }

    const fmMatch = note.content.match(/^---\n([\s\S]*?)\n---\n?/);
    let copyContent: string;
    if (fmMatch) {
      let fm: Record<string, unknown>;
      try { fm = (parseYaml(fmMatch[1]) as Record<string, unknown>) ?? {}; } catch { fm = {}; }
      fm['pubobs-parent'] = note.path;
      const fmStr = stringifyYaml(fm);
      copyContent = `---\n${fmStr}---\n${note.content.slice(fmMatch[0].length)}`;
    } else {
      const fmStr = stringifyYaml({ 'pubobs-parent': note.path });
      copyContent = `---\n${fmStr}---\n${note.content}`;
    }

    const dir = copyPath.split('/').slice(0, -1).join('/');
    if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
      await this.app.vault.createFolder(dir);
    }

    const existing = this.app.vault.getAbstractFileByPath(copyPath);
    if (existing instanceof TFile) {
      await this.app.vault.modify(existing, copyContent);
    } else {
      await this.app.vault.create(copyPath, copyContent);
    }
  }

  private async buildSyncFile(
    file: TFile,
    content: string,
    vaultFolder: string,
    subfolder: string,
    repoId: string,
    detectedPluginIds: string[] = [],
  ): Promise<{ syncFile: SyncFile; assets: Map<string, ArrayBuffer> }> {
    const cache = this.app.metadataCache.getFileCache(file);
    const { position: _pos, ...frontmatter } = ((cache?.frontmatter ?? {}) as Record<string, unknown>);

    let relative = file.path;
    if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
      relative = relative.slice(vaultFolder.length + 1);
    }
    const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const manifests = (this.app as any).plugins?.manifests ?? {};
    const pluginsMeta = detectedPluginIds.map(id => ({
      id,
      version: (manifests[id]?.version as string | undefined) ?? '0.0.0',
    }));

    if (pluginsMeta.length > 0) {
      frontmatter['pubobs-plugins'] = pluginsMeta;
    } else {
      delete frontmatter['pubobs-plugins'];
    }

    let mdContent = injectPluginFrontmatter(content, pluginsMeta);

    const { html, assets } = await renderNoteToHTML(this.app, content, file.path, repoId, vaultFolder, subfolder);

    // ── Stable per-note encryption key ──────────────────────────────────────────
    if (!this.settings.renderKeys[repoId]) this.settings.renderKeys[repoId] = {};
    let renderKeyB64 = this.settings.renderKeys[repoId][repoPath];
    if (!renderKeyB64) {
      // Fallback: check existing frontmatter in case settings was migrated or lost
      const fmMatch = mdContent.match(/^---\n([\s\S]*?)\n---/);
      if (fmMatch) {
        try {
          const fm = parseYaml(fmMatch[1]) as Record<string, unknown>;
          if (typeof fm['pubobs-render-key'] === 'string') {
            renderKeyB64 = fm['pubobs-render-key'];
          }
        } catch { /* ignore */ }
      }
    }
    if (!renderKeyB64) {
      renderKeyB64 = base64urlEncode(crypto.getRandomValues(new Uint8Array(32)));
    }
    this.settings.renderKeys[repoId][repoPath] = renderKeyB64;

    const renderURL = `${this.settings.backendUrl.replace(/\/$/, '')}/pub/${repoId}/render/${repoPath}`;
    mdContent = injectFrontmatterFields(mdContent, {
      'pubobs-render-url': renderURL,
      'pubobs-render-key': renderKeyB64,
    });

    // Also reflect the injected fields in the frontmatter payload so the backend
    // can store them in MetadataJSON immediately (without waiting for git to commit)
    frontmatter['pubobs-render-url'] = renderURL;
    frontmatter['pubobs-render-key'] = renderKeyB64;

    const encryptedHTML = await encryptHTML(html, base64urlDecode(renderKeyB64));

    return {
      syncFile: { path: repoPath, md_content: mdContent, encrypted_html: encryptedHTML, frontmatter },
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

// FNV-1a 32-bit hash — fast, good enough for change detection
function fnv1a(s: string): string {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h.toString(36);
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
  { id: 'obsidian-outliner',           name: 'Outliner',         patterns: [] }, // no detectable syntax
];

function detectPlugins(content: string): string[] {
  return PLUGIN_PATTERNS
    .filter(p => p.patterns.length > 0 && p.patterns.some(re => re.test(content)))
    .map(p => p.id);
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
