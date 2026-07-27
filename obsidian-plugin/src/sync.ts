import { App, TFile, Notice, Modal, parseYaml, stringifyYaml } from 'obsidian';
import type { BackendClient, SyncFile, SyncAsset, SyncDataFile } from './client';
import type { PubObsSettings } from './types';
import { renderNoteToHTML, extractStyles } from './renderer';
import { parseDataFileExtensions, isDataFilePath, utf8ByteLength } from './datafiles';

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

// ── SyncManager ───────────────────────────────────────────────────────────────

export class SyncManager {
  constructor(
    private app: App,
    private client: BackendClient,
    private settings: PubObsSettings,
    private saveSettings: () => Promise<void>,
  ) {}

  async syncRepo(repoId: string, opts?: { force?: boolean }): Promise<void> {
    const force = opts?.force ?? false;
    const mapping = this.settings.repoMappings[repoId];
    if (!mapping) throw new Error(`No folder mapping for repo ${repoId}`);

    const { vaultFolder, subfolder } = mapping;

    if (!vaultFolder) {
      new Notice(`PubObs: set the vault folder for "${mapping.repoName}" in settings before syncing.`);
      return;
    }

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

        // Don't overwrite local unsynchronised changes with the server version.
        // If the local file hash differs from what was last pushed, the user has
        // made edits since the last sync — skip the pull to preserve them.
        const vaultPathForCheck = repoPathToVaultPath(file.path, vaultFolder, subfolder);
        const existingForCheck = this.app.vault.getAbstractFileByPath(vaultPathForCheck);
        if (existingForCheck instanceof TFile) {
          const localContent = await this.app.vault.read(existingForCheck);
          const lastSyncedHash = (this.settings.syncHashes[repoId] ?? {})[file.path];
          if (lastSyncedHash !== undefined && fnv1a(localContent) !== lastSyncedHash) {
            continue;
          }
        } else if (
          (this.settings.syncHashes[repoId] ?? {})[file.path] !== undefined
          || (this.settings.pullSHAs[repoId] ?? {})[file.path] !== undefined
        ) {
          // No local file exists at this path, but this vault previously
          // pushed or pulled it (syncHashes / pullSHAs entry). That means
          // the file was deleted or renamed locally and the deletion just
          // hasn't reached the remote yet (e.g. a prior push failed) — NOT
          // that this is genuine new content authored elsewhere. Treat it as
          // a pending deletion: don't resurrect it here, and drop any stale
          // pull-SHA cache for it. The push phase below will still see this
          // path missing from the vault and re-attempt the deletion on the
          // remote.
          delete storedPullSHAs[file.path];
          continue;
        }

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
      let pulledData = 0;
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

      // Data files are pulled in their own try/catch: a backend that predates
      // this feature returns 404 here, and that must degrade to notes-only
      // syncing rather than failing the whole pull.
      try {
        const exts = parseDataFileExtensions(this.settings.dataFileExtensions ?? '');
        if (exts.length > 0) {
          const maxBytes = Math.max(1, this.settings.dataFileMaxMB ?? 5) * 1024 * 1024;
          const { files: dataFiles, skipped } = await this.client.listDataFiles(repoId, exts, maxBytes);

          for (const file of dataFiles) {
            if (storedPullSHAs[file.path] === file.sha) continue;

            const vaultPath = repoPathToVaultPath(file.path, vaultFolder, subfolder);
            const existing = this.app.vault.getAbstractFileByPath(vaultPath);
            if (existing instanceof TFile) {
              // Same protection notes get: never overwrite edits that were
              // made locally and haven't been pushed yet.
              const localContent = await this.app.vault.read(existing);
              const lastSyncedHash = (this.settings.syncHashes[repoId] ?? {})[file.path];
              if (lastSyncedHash !== undefined && fnv1a(localContent) !== lastSyncedHash) {
                continue;
              }
              await this.app.vault.modify(existing, file.content);
            } else {
              const dir = vaultPath.split('/').slice(0, -1).join('/');
              if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
                await this.app.vault.createFolder(dir);
              }
              await this.app.vault.create(vaultPath, file.content);
            }
            storedPullSHAs[file.path] = file.sha;
            pulledData++;
          }

          if (skipped.length > 0) {
            const names = skipped.map(s => `${s.path} (${s.reason === 'too_large' ? 'too large' : 'not text'})`);
            new Notice(`PubObs: ${skipped.length} data file(s) skipped — ${names.join(', ')}`, 10000);
          }

          this.settings.pullSHAs[repoId] = storedPullSHAs;
          await this.saveSettings();
        }
      } catch (e) {
        const reason = e instanceof Error ? e.message : String(e);
        console.error('[PubObs] data file pull failed:', e);
        new Notice(`PubObs: data files not synced — ${reason}`, 10000);
      }

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

      if (pulled > 0 || pulledData > 0) {
        notice.setMessage(`PubObs: pulled ${pulled} note(s), ${pulledData} data file(s), pushing local changes…`);
      }
    } catch (e) {
      const reason = e instanceof Error ? e.message : String(e);
      console.error('[PubObs] pull phase failed:', e);
      new Notice(`PubObs: pull failed — ${reason}. Continuing with local push…`, 10000);
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
    const failedFiles: Array<{ path: string; reason: string }> = [];

    for (let i = 0; i < vaultFiles.length; i++) {
      const f = vaultFiles[i];

      // Record the path as present BEFORE any fallible work (reading or
      // rendering). currentRepoPaths drives deletion detection below: a path
      // in knownPaths but missing here is treated as a deletion and
      // propagated to git + DB + render store. A file that genuinely exists
      // but momentarily fails to read must never be mistaken for a deletion,
      // so its presence can't hinge on the read succeeding. The path is pure
      // string math off f.path, so it can't throw and is safe outside try.
      let relative = f.path;
      if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
        relative = relative.slice(vaultFolder.length + 1);
      }
      const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;
      currentRepoPaths.add(repoPath);

      try {
        const content = await this.app.vault.read(f);
        const hash = fnv1a(content);
        newHashes[repoPath] = hash;

        if (!force && storedHashes[repoPath] === hash) {
          skipped++;
          continue;
        }

        notice.setMessage(`Rendering ${syncFiles.length + 1}: ${f.basename}…`);

        // A file that was created/modified/renamed moments ago may not have
        // been indexed by Obsidian's metadata cache yet. Rendering it too
        // early can trip up community plugins (e.g. Dataview) that assume
        // the cache already exists for the file they're processing, surfacing
        // as an unrelated-looking "no Obsidian file metadata" crash. Give
        // Obsidian a bounded window to finish indexing before we touch it.
        const metadataReady = await this.waitForMetadataReady(f);
        if (!metadataReady) {
          console.warn(`[PubObs] metadata cache still not ready for "${f.path}" after waiting — proceeding anyway`);
        }

        const used = detectPlugins(content);
        const { syncFile, assets } = await this.buildSyncFile(f, content, vaultFolder, subfolder, repoId, used);

        syncFiles.push(syncFile);
        for (const [vaultPath, buf] of assets) assetMap.set(vaultPath, buf);
      } catch (e) {
        const reason = e instanceof Error ? e.message : String(e);
        failedFiles.push({ path: f.path, reason });
        console.error(`[PubObs] failed to sync "${f.path}": ${reason}`, e);
        new Notice(`PubObs: skipped "${f.basename}" — ${reason}`, 10000);
      }
    }

    // ── Data files ────────────────────────────────────────────────────────────
    // Enumerated here, in the same phase as notes, because currentRepoPaths
    // drives deletion detection: a path known from a previous sync but missing
    // from this set is reported in deletedPaths and removed from the repo.
    // Data files live in the same syncHashes map as notes, so if they were not
    // registered here every one of them would be deleted on the next sync.
    const dataExts = parseDataFileExtensions(this.settings.dataFileExtensions ?? '');
    const dataMaxMB = this.settings.dataFileMaxMB ?? 5;
    const dataMaxBytes = Math.max(1, dataMaxMB) * 1024 * 1024;
    const syncDataFiles: SyncDataFile[] = [];
    const oversized: string[] = [];

    if (dataExts.length > 0) {
      const vaultDataFiles = this.app.vault
        .getFiles()
        .filter((f: TFile) =>
          isDataFilePath(f.path, dataExts) &&
          (vaultFolder === '' || f.path.startsWith(vaultFolder + '/')));

      for (const f of vaultDataFiles) {
        let relative = f.path;
        if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
          relative = relative.slice(vaultFolder.length + 1);
        }
        const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;
        currentRepoPaths.add(repoPath);

        try {
          const content = await this.app.vault.read(f);
          const bytes = utf8ByteLength(content);
          if (bytes > dataMaxBytes) {
            oversized.push(`${f.path} (${(bytes / 1024 / 1024).toFixed(1)} MB)`);
            // Keep the hash of what the repo actually holds, since we are
            // not changing it: this is what makes a revert to that same
            // last-pushed content compare equal and correctly skip next time.
            newHashes[repoPath] = storedHashes[repoPath] ?? '';
            continue;
          }
          const hash = fnv1a(content);
          newHashes[repoPath] = hash;
          if (!force && storedHashes[repoPath] === hash) {
            skipped++;
            continue;
          }
          syncDataFiles.push({ path: repoPath, content });
        } catch (e) {
          const reason = e instanceof Error ? e.message : String(e);
          failedFiles.push({ path: f.path, reason });
          console.error(`[PubObs] failed to read data file "${f.path}": ${reason}`, e);
        }
      }
    }

    if (oversized.length > 0) {
      new Notice(
        `PubObs: ${oversized.length} data file(s) too large for the ${dataMaxMB} MB limit — ${oversized.join(', ')}`,
        10000,
      );
    }

    notice.hide();

    // Paths this vault previously pushed, pulled, or minted a key for but
    // that no longer exist locally — must be reported as deletions even
    // when syncHashes is empty (e.g. a pulled-then-deleted file never got
    // a push hash).
    const knownPaths = new Set<string>(Object.keys(storedHashes));
    for (const p of Object.keys(this.settings.pullSHAs[repoId] ?? {})) {
      knownPaths.add(p);
    }
    for (const p of Object.keys(this.settings.noteKeys?.[repoId] ?? {})) {
      knownPaths.add(p);
    }
    const deletedPaths = [...knownPaths].filter(p => {
      if (currentRepoPaths.has(p)) return false;
      // A path this sync no longer enumerates is not a deletion. Narrowing or
      // clearing the data-file extension setting means "stop syncing these",
      // never "delete them from the repo" — a filter must not destroy data.
      if (!p.endsWith('.md') && !isDataFilePath(p, dataExts)) return false;
      return true;
    });

    // Remove cached state for notes that no longer exist
    if (this.settings.noteKeys?.[repoId]) {
      for (const p of deletedPaths) {
        delete this.settings.noteKeys[repoId][p];
      }
    }
    for (const p of deletedPaths) {
      if (this.settings.pullSHAs[repoId]) {
        delete this.settings.pullSHAs[repoId][p];
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

    const failedSuffix = failedFiles.length > 0
      ? `, ${failedFiles.length} skipped due to errors (see console)`
      : '';

    if (syncFiles.length === 0 && syncDataFiles.length === 0 && deletedPaths.length === 0) {
      if (failedFiles.length > 0) {
        new Notice(`PubObs: nothing changed (${skipped} note(s) up to date${failedSuffix})`);
      } else {
        new Notice(`PubObs: nothing changed (${skipped} note(s) up to date)`);
      }
      return;
    }

    console.log(`[PubObs] syncing ${syncFiles.length} changed, ${deletedPaths.length} deleted, ${skipped} unchanged, ${failedFiles.length} failed`);
    try {
      const result = await this.client.sync(repoId, syncFiles, syncAssets, deletedPaths, syncDataFiles);

      // The backend is the sole authority on each note's key — always
      // overwrite our local cache with whatever it just returned, even for
      // notes we thought we already had a key for (e.g. an admin
      // unshared/rotated the key via the web UI since our last sync).
      if (result.note_keys) {
        if (!this.settings.noteKeys) this.settings.noteKeys = {};
        if (!this.settings.noteKeys[repoId]) this.settings.noteKeys[repoId] = {};
        for (const [path, key] of Object.entries(result.note_keys)) {
          this.settings.noteKeys[repoId][path] = key;
        }
      }

      // Persist hashes only after a successful sync
      for (const p of deletedPaths) delete newHashes[p];
      this.settings.syncHashes[repoId] = newHashes;
      await this.saveSettings();

      const dataSuffix = syncDataFiles.length > 0 ? `, ${syncDataFiles.length} data file(s)` : '';
      new Notice(`PubObs: ${syncFiles.length} synced${dataSuffix}, ${deletedPaths.length} deleted, ${skipped} unchanged${failedSuffix} — ${result.commit_sha.slice(0, 7)}`);

      // The server enforces its own 25 MB ceiling regardless of this client's
      // setting, and reports anything it dropped. A sync that "succeeded"
      // while quietly omitting a file from the commit is exactly the case
      // this surfaces — never let it pass silently.
      if (result.skipped_paths && result.skipped_paths.length > 0) {
        const names = result.skipped_paths.map(s => s.path).join(', ');
        new Notice(`PubObs: the server skipped ${result.skipped_paths.length} file(s) — ${names}`, 10000);
      }
    } catch (e) {
      const err = e as any;
      const reason = e instanceof Error ? e.message : String(e);
      // A 403 can mean strict-mode "no git credential configured", an ordinary
      // authorization denial (e.g. the editor role was revoked), or the git
      // remote refusing the push (a token without write access to this repo, a
      // protected branch). Only the first is fixed in Settings, and it is the
      // only one phrased as an instruction to configure one — so match that
      // wording specifically. A looser /credential/i test also swallowed the
      // remote-refusal message, whose quoted reason from the git host is the
      // only thing that says what actually needs fixing.
      if (err?.status === 403 && /configure your git credential/i.test(reason)) {
        new Notice(`PubObs: configure your git credential for "${mapping.repoName}" in Settings before publishing.`, 10000);
      } else {
        console.error('[PubObs] sync failed:', e);
        new Notice(`PubObs: sync failed — ${reason}`, 10000);
      }
    }
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

  /**
   * Waits for Obsidian to finish indexing `file`'s metadata cache, up to
   * `timeoutMs`. Resolves `true` as soon as `getFileCache` returns a value,
   * or `false` if the timeout elapses first — callers should treat `false`
   * as "proceed anyway, best-effort" rather than a hard failure, since most
   * of what we read from the cache (frontmatter) is optional.
   */
  private waitForMetadataReady(file: TFile, timeoutMs = 4000): Promise<boolean> {
    if (this.app.metadataCache.getFileCache(file)) return Promise.resolve(true);

    return new Promise<boolean>(resolve => {
      let settled = false;
      const finish = (ready: boolean) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        this.app.metadataCache.offref(ref);
        resolve(ready);
      };
      const ref = this.app.metadataCache.on('resolve', resolvedFile => {
        if (resolvedFile.path === file.path && this.app.metadataCache.getFileCache(file)) {
          finish(true);
        }
      });
      const timer = setTimeout(() => finish(!!this.app.metadataCache.getFileCache(file)), timeoutMs);
    });
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

    const mdContent = injectPluginFrontmatter(content, pluginsMeta);

    // ── Backend-authoritative encryption key ────────────────────────────────────
    // Prefer a cached key from an earlier sync's response. For a brand-new
    // note (no cached key yet — first sync ever, or plugin data was
    // cleared), fetch-or-mint one from the backend BEFORE we can encrypt
    // this file's payload: the main sync request can't mint it for us here,
    // since by the time the server would generate it, the client already
    // needs to have sent the (necessarily already-encrypted) content.
    //
    // Fetch the key *before* rendering so a backend/key failure skips the
    // expensive (and potentially crash-prone) MarkdownRenderer pass for
    // this file — per-file isolation then reports it as a skip Notice.
    if (!this.settings.noteKeys) this.settings.noteKeys = {};
    if (!this.settings.noteKeys[repoId]) this.settings.noteKeys[repoId] = {};
    let keyB64 = this.settings.noteKeys[repoId][repoPath];
    if (!keyB64) {
      keyB64 = await this.client.getNoteKey(repoId, repoPath);
      this.settings.noteKeys[repoId][repoPath] = keyB64;
    }

    const { html, assets } = await renderNoteToHTML(this.app, content, file.path, repoId, vaultFolder, subfolder, this.settings.noteKeys?.[repoId]);

    const encryptedHTML = await encryptHTML(html, base64urlDecode(keyB64));

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
