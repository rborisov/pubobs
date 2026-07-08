// Mock renderer to avoid DOM dependency (document.styleSheets)
jest.mock('../src/renderer', () => ({
  renderNoteToHTML: jest.fn().mockResolvedValue({ html: '<p>mock</p>', assets: new Map() }),
  extractStyles: jest.fn().mockReturnValue(''),
}));

// Mock obsidian before importing sync so the module loads without errors
jest.mock('obsidian', () => ({
  App: class {},
  TFile: class {},
  __notices: [] as string[],
  Notice: class {
    constructor(message?: string) {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      (require('obsidian').__notices as string[]).push(message ?? '');
    }
    setMessage() {}
    hide() {}
  },
  Modal: class {
    constructor(public app: unknown) {}
    open() {}
    close() {}
    get contentEl() { return { createEl: () => ({ createEl: () => {} }), createDiv: () => ({ createEl: () => {} }), empty: () => {} }; }
  },
  parseYaml: (s: string) => {
    // Minimal YAML parser for tests: handle simple key: value pairs and lists
    const result: Record<string, unknown> = {};
    const lines = s.split('\n');
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      const kv = line.match(/^([^:]+):\s*(.*)/);
      if (kv) {
        const key = kv[1].trim();
        const val = kv[2].trim();
        if (val === '' || val === '|' || val === '>') {
          // Could be a list or block — collect child lines
          const children: Array<Record<string, string>> = [];
          i++;
          let obj: Record<string, string> = {};
          while (i < lines.length && (lines[i].startsWith('  ') || lines[i].startsWith('\t'))) {
            const child = lines[i].trim();
            if (child.startsWith('- ')) {
              if (Object.keys(obj).length > 0) { children.push(obj); obj = {}; }
              const rest = child.slice(2);
              const m = rest.match(/^([^:]+):\s*(.*)/);
              if (m) obj[m[1].trim()] = m[2].trim().replace(/^["']|["']$/g, '');
            } else {
              const m = child.match(/^([^:]+):\s*(.*)/);
              if (m) obj[m[1].trim()] = m[2].trim().replace(/^["']|["']$/g, '');
            }
            i++;
          }
          if (Object.keys(obj).length > 0) children.push(obj);
          result[key] = children.length > 0 ? children : undefined;
          continue;
        } else {
          result[key] = val.replace(/^["']|["']$/g, '');
        }
      }
      i++;
    }
    return result;
  },
  stringifyYaml: (obj: Record<string, unknown>) => {
    let out = '';
    for (const [k, v] of Object.entries(obj)) {
      if (Array.isArray(v)) {
        out += `${k}:\n`;
        for (const item of v) {
          if (typeof item === 'object' && item !== null) {
            const entries = Object.entries(item as Record<string, unknown>);
            entries.forEach(([ik, iv], idx) => {
              out += idx === 0 ? `  - ${ik}: ${iv}\n` : `    ${ik}: ${iv}\n`;
            });
          } else {
            out += `  - ${item}\n`;
          }
        }
      } else {
        out += `${k}: ${v}\n`;
      }
    }
    return out;
  },
}));

import { repoPathToVaultPath, SyncManager, semverGte, injectPluginFrontmatter, parseFrontmatterPlugins } from '../src/sync';
import { renderNoteToHTML } from '../src/renderer';

describe('repoPathToVaultPath', () => {
  test('no vaultFolder, no subfolder — returns path unchanged', () => {
    expect(repoPathToVaultPath('notes/foo.md', '', '')).toBe('notes/foo.md');
  });

  test('vaultFolder only — prepends folder', () => {
    expect(repoPathToVaultPath('foo.md', 'Published', '')).toBe('Published/foo.md');
  });

  test('subfolder only — strips subfolder prefix', () => {
    expect(repoPathToVaultPath('posts/foo.md', '', 'posts')).toBe('foo.md');
  });

  test('both vaultFolder and subfolder — strips subfolder, prepends vaultFolder', () => {
    expect(repoPathToVaultPath('posts/foo.md', 'Published', 'posts')).toBe('Published/foo.md');
  });

  test('subfolder with trailing slash is normalized', () => {
    expect(repoPathToVaultPath('posts/foo.md', 'Published', 'posts/')).toBe('Published/foo.md');
  });

  test('nested path with subfolder', () => {
    expect(repoPathToVaultPath('posts/2026/05/foo.md', 'Published', 'posts')).toBe('Published/2026/05/foo.md');
  });

  test('file not under subfolder — returned as-is under vaultFolder', () => {
    expect(repoPathToVaultPath('other/bar.md', 'Published', 'posts')).toBe('Published/other/bar.md');
  });
});

describe('semverGte', () => {
  test('equal versions', () => expect(semverGte('1.2.3', '1.2.3')).toBe(true));
  test('installed higher patch', () => expect(semverGte('1.2.4', '1.2.3')).toBe(true));
  test('installed lower patch', () => expect(semverGte('1.2.2', '1.2.3')).toBe(false));
  test('installed higher minor', () => expect(semverGte('1.3.0', '1.2.9')).toBe(true));
  test('installed lower minor', () => expect(semverGte('1.1.9', '1.2.0')).toBe(false));
  test('installed higher major', () => expect(semverGte('2.0.0', '1.9.9')).toBe(true));
  test('installed lower major', () => expect(semverGte('1.9.9', '2.0.0')).toBe(false));
});

describe('injectPluginFrontmatter', () => {
  test('adds pubobs-plugins to existing frontmatter', () => {
    const content = '---\ntitle: Test\n---\n\n# Hello';
    const plugins = [{ id: 'dataview', version: '0.5.55' }];
    const result = injectPluginFrontmatter(content, plugins);
    expect(result).toContain('pubobs-plugins:');
    expect(result).toContain('dataview');
    expect(result).toContain('# Hello');
  });

  test('creates frontmatter when absent', () => {
    const content = '# Hello\nNo frontmatter';
    const plugins = [{ id: 'dataview', version: '0.5.55' }];
    const result = injectPluginFrontmatter(content, plugins);
    expect(result.startsWith('---\n')).toBe(true);
    expect(result).toContain('pubobs-plugins:');
    expect(result).toContain('# Hello');
  });

  test('removes pubobs-plugins when no plugins detected', () => {
    const content = '---\ntitle: Test\npubobs-plugins:\n  - id: dataview\n    version: 0.5.55\n---\n\n# Hello';
    const result = injectPluginFrontmatter(content, []);
    expect(result).not.toContain('pubobs-plugins');
    expect(result).toContain('title: Test');
  });

  test('no-op when no plugins and no existing pubobs-plugins', () => {
    const content = '---\ntitle: Test\n---\n\n# Hello';
    const result = injectPluginFrontmatter(content, []);
    expect(result).toBe(content);
  });
});

describe('parseFrontmatterPlugins', () => {
  test('returns empty for note without frontmatter', () => {
    expect(parseFrontmatterPlugins('# Hello')).toEqual([]);
  });

  test('returns empty for frontmatter without pubobs-plugins', () => {
    expect(parseFrontmatterPlugins('---\ntitle: Test\n---\n# Hello')).toEqual([]);
  });

  test('returns plugins from frontmatter', () => {
    const content = '---\npubobs-plugins:\n  - id: dataview\n    version: "0.5.55"\n---\n# Hello';
    expect(parseFrontmatterPlugins(content)).toEqual([{ id: 'dataview', version: '0.5.55' }]);
  });

  test('returns multiple plugins', () => {
    const content = '---\npubobs-plugins:\n  - id: dataview\n    version: "0.5.55"\n  - id: templater-obsidian\n    version: "1.16.0"\n---\n# Hello';
    const result = parseFrontmatterPlugins(content);
    expect(result).toHaveLength(2);
    expect(result[0].id).toBe('dataview');
    expect(result[1].id).toBe('templater-obsidian');
  });
});

describe('SyncManager.syncRepo', () => {
  function makeMockApp(vaultFiles: Record<string, boolean> = {}) {
    const TFileMock = (jest.requireMock('obsidian') as any).TFile;
    return {
      vault: {
        getFiles: jest.fn().mockReturnValue([]),
        getAbstractFileByPath: jest.fn((path: string) => {
          if (vaultFiles[path]) return new TFileMock();
          return null;
        }),
        create: jest.fn().mockResolvedValue(undefined),
        modify: jest.fn().mockResolvedValue(undefined),
        createFolder: jest.fn().mockResolvedValue(undefined),
        read: jest.fn().mockResolvedValue(''),
      },
      metadataCache: {
        getFileCache: jest.fn().mockReturnValue(null),
      },
    };
  }

  function makeMockClient(files: Array<{ path: string; content: string; sha: string }> = []) {
    return {
      listFiles: jest.fn().mockResolvedValue(files),
      sync: jest.fn().mockResolvedValue({ commit_sha: 'abc1234567890' }),
    };
  }

  function makeSettings(pullSHAs: Record<string, Record<string, string>> = {}) {
    return {
      repoMappings: { 'repo-1': { repoName: 'Test', vaultFolder: 'Published', subfolder: '' } },
      pullSHAs,
      syncHashes: {},
    };
  }

  test('pulls new file from remote during pull phase', async () => {
    const app = makeMockApp();
    const client = makeMockClient([{ path: 'notes/foo.md', content: '# Foo', sha: 'abc' }]);
    const settings = makeSettings();
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    expect(app.vault.create).toHaveBeenCalledWith('Published/notes/foo.md', '# Foo');
    expect(settings.pullSHAs['repo-1']['notes/foo.md']).toBe('abc');
  });

  test('skips pull when SHA matches stored SHA', async () => {
    const app = makeMockApp();
    const client = makeMockClient([{ path: 'notes/foo.md', content: '# Foo', sha: 'abc' }]);
    const settings = makeSettings({ 'repo-1': { 'notes/foo.md': 'abc' } });
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    expect(app.vault.create).not.toHaveBeenCalled();
    expect(app.vault.modify).not.toHaveBeenCalled();
  });

  test('filters out _pubobs/ files during pull', async () => {
    const app = makeMockApp();
    const client = makeMockClient([
      { path: '_pubobs/obsidian.css', content: 'body{}', sha: 'css-sha' },
      { path: 'notes/real.md', content: '# Real', sha: 'md-sha' },
    ]);
    const settings = makeSettings();
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    expect(app.vault.create).toHaveBeenCalledTimes(1);
    expect(app.vault.create).toHaveBeenCalledWith('Published/notes/real.md', '# Real');
  });

  test('throws when no repo mapping exists', async () => {
    const app = makeMockApp();
    const client = makeMockClient([]);
    const settings = { repoMappings: {}, pullSHAs: {}, syncHashes: {} };
    const save = jest.fn();
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await expect(manager.syncRepo('missing-repo')).rejects.toThrow('No folder mapping');
  });

  // Regression tests for a real data-corruption bug: a file previously
  // pushed by this vault (tracked in syncHashes) that was later deleted or
  // renamed locally, but whose deletion never reached the remote (e.g. a
  // prior push failed), must NOT be resurrected into the vault by the next
  // pull. Before the fix, the pull phase only guarded against clobbering
  // local edits when a local file *existed*; when it was absent entirely
  // (the deleted/renamed case), it unconditionally recreated it from the
  // stale remote copy — and once recreated, the push phase immediately
  // after would see it back in the vault and stop treating it as deleted,
  // permanently losing the deletion signal.
  test('does not recreate a locally-deleted file this vault previously owned (syncHashes entry, no local file)', async () => {
    const app = makeMockApp(); // no local files exist
    const client = makeMockClient([
      { path: 'notes/old-name.md', content: '# Old', sha: 'old-sha' },
    ]);
    const settings = makeSettings();
    // This vault previously pushed notes/old-name.md (e.g. before it was
    // renamed locally), but that push's deletion of the old path never
    // reached the remote, so it's still listed there with a differing sha.
    (settings as any).syncHashes = { 'repo-1': { 'notes/old-name.md': 'some-old-content-hash' } };
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    expect(app.vault.create).not.toHaveBeenCalled();
    expect(app.vault.modify).not.toHaveBeenCalled();
  });

  test('still pulls a genuinely new remote-only path with no prior syncHashes entry', async () => {
    const app = makeMockApp(); // no local files exist
    const client = makeMockClient([
      { path: 'notes/brand-new.md', content: '# New', sha: 'new-sha' },
    ]);
    const settings = makeSettings();
    // No syncHashes entry at all for this path — this vault never owned it,
    // so it's real new content added via git/another client and should
    // still be pulled down normally.
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    expect(app.vault.create).toHaveBeenCalledWith('Published/notes/brand-new.md', '# New');
    expect(settings.pullSHAs['repo-1']['notes/brand-new.md']).toBe('new-sha');
  });

  test('shows a Notice when the pull phase fails but still attempts push', async () => {
    const app = makeMockApp();
    app.vault.getFiles = jest.fn().mockReturnValue([]);
    const client = {
      listFiles: jest.fn().mockRejectedValue(new Error('Server returned a non-JSON response (HTTP 502)')),
      sync: jest.fn().mockResolvedValue({ commit_sha: 'abc1234567890' }),
    };
    const settings = makeSettings();
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    const notices = (jest.requireMock('obsidian') as { __notices: string[] }).__notices;
    expect(notices.some(n => n.includes('pull failed') && n.includes('502'))).toBe(true);
  });

  test('still skips pulling over unsynced local edits when the file exists locally', async () => {
    const app = makeMockApp({ 'Published/notes/edited.md': true });
    app.vault.read = jest.fn().mockResolvedValue('# Locally edited content');
    const client = makeMockClient([
      { path: 'notes/edited.md', content: '# Remote content', sha: 'remote-sha' },
    ]);
    const settings = makeSettings();
    // lastSyncedHash won't match fnv1a of the current local content, since
    // the local file has been edited since the last successful push.
    (settings as any).syncHashes = { 'repo-1': { 'notes/edited.md': 'stale-hash-from-last-push' } };
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.syncRepo('repo-1');

    expect(app.vault.modify).not.toHaveBeenCalled();
    expect(app.vault.create).not.toHaveBeenCalled();
  });
});

describe('SyncManager key handling', () => {
  // Valid base64url-encoded 32-byte AES-256 keys — encryptHTML's
  // crypto.subtle.importKey call rejects anything the wrong length, so
  // these can't be arbitrary test strings like real code elsewhere uses.
  const FETCHED_KEY = 'AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE';
  const CACHED_KEY = 'AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI';
  const SERVER_ROTATED_KEY = 'AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM';
  const STALE_LOCAL_KEY = 'BAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQ';

  function makeMockFile(path: string) {
    const base = path.slice(path.lastIndexOf('/') + 1);
    return { path, extension: 'md', basename: base.replace(/\.md$/, '') };
  }

  function makeMockApp(files: unknown[], content = '# Hello') {
    return {
      vault: {
        getFiles: jest.fn().mockReturnValue(files),
        getAbstractFileByPath: jest.fn().mockReturnValue(null),
        create: jest.fn().mockResolvedValue(undefined),
        modify: jest.fn().mockResolvedValue(undefined),
        createFolder: jest.fn().mockResolvedValue(undefined),
        read: jest.fn().mockResolvedValue(content),
      },
      metadataCache: {
        getFileCache: jest.fn().mockReturnValue({ frontmatter: {} }),
        getFirstLinkpathDest: jest.fn().mockReturnValue(null),
      },
    };
  }

  function makeMockClient(noteKeys: Record<string, string> = { 'notes/foo.md': FETCHED_KEY }) {
    return {
      listFiles: jest.fn().mockResolvedValue([]),
      getNoteKey: jest.fn().mockResolvedValue(FETCHED_KEY),
      sync: jest.fn().mockResolvedValue({ commit_sha: 'abc1234567890', note_keys: noteKeys }),
    };
  }

  function makeSettings() {
    return {
      repoMappings: { 'repo-1': { repoName: 'Test', vaultFolder: 'Published', subfolder: '' } },
      pullSHAs: {},
      syncHashes: {},
      noteKeys: {} as Record<string, Record<string, string>>,
    };
  }

  test('fetches a key from the backend for a brand-new note before encrypting', async () => {
    const app = makeMockApp([makeMockFile('Published/notes/foo.md')]);
    const client = makeMockClient();
    const settings = makeSettings();
    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));

    await manager.syncRepo('repo-1');

    expect(client.getNoteKey).toHaveBeenCalledWith('repo-1', 'notes/foo.md');
    expect(settings.noteKeys['repo-1']['notes/foo.md']).toBe(FETCHED_KEY);
  });

  test('reuses a cached key without calling getNoteKey again', async () => {
    const app = makeMockApp([makeMockFile('Published/notes/foo.md')]);
    const client = makeMockClient({ 'notes/foo.md': CACHED_KEY });
    const settings = makeSettings();
    settings.noteKeys = { 'repo-1': { 'notes/foo.md': CACHED_KEY } };
    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));

    await manager.syncRepo('repo-1');

    expect(client.getNoteKey).not.toHaveBeenCalled();
  });

  test('overwrites the local key cache with whatever the sync response returns, even if a local guess existed', async () => {
    const app = makeMockApp([makeMockFile('Published/notes/foo.md')]);
    const client = makeMockClient({ 'notes/foo.md': SERVER_ROTATED_KEY });
    const settings = makeSettings();
    settings.noteKeys = { 'repo-1': { 'notes/foo.md': STALE_LOCAL_KEY } };
    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));

    await manager.syncRepo('repo-1');

    expect(settings.noteKeys['repo-1']['notes/foo.md']).toBe(SERVER_ROTATED_KEY);
  });

  test('never writes a key or pubobs-url back into the vault file', async () => {
    const app = makeMockApp([makeMockFile('Published/notes/foo.md')]);
    const client = makeMockClient();
    const settings = makeSettings();
    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));

    await manager.syncRepo('repo-1');

    expect(app.vault.modify).not.toHaveBeenCalled();
  });
});

describe('SyncManager metadata readiness and per-file isolation', () => {
  const KEY = 'AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE';

  function makeMockFile(path: string) {
    const base = path.slice(path.lastIndexOf('/') + 1);
    return { path, extension: 'md', basename: base.replace(/\.md$/, '') };
  }

  // Mimics Obsidian's MetadataCache: getFileCache returns null for a file
  // that hasn't been indexed yet, and an 'resolve' event fires once it has —
  // exactly the readiness signal SyncManager waits on for very recently
  // created/modified files.
  function makeMetadataCache(readyPaths: Set<string> = new Set()) {
    const listeners: Array<(file: { path: string }) => void> = [];
    return {
      getFileCache: jest.fn((file: { path: string }) => (readyPaths.has(file.path) ? { frontmatter: {} } : null)),
      getFirstLinkpathDest: jest.fn().mockReturnValue(null),
      on: jest.fn((_event: string, cb: (file: { path: string }) => void) => {
        listeners.push(cb);
        return {};
      }),
      offref: jest.fn(),
      __emitResolve(file: { path: string }) {
        readyPaths.add(file.path);
        for (const cb of listeners) cb(file);
      },
    };
  }

  function makeMockApp(files: unknown[], metadataCache: ReturnType<typeof makeMetadataCache>) {
    return {
      vault: {
        getFiles: jest.fn().mockReturnValue(files),
        getAbstractFileByPath: jest.fn().mockReturnValue(null),
        create: jest.fn().mockResolvedValue(undefined),
        modify: jest.fn().mockResolvedValue(undefined),
        createFolder: jest.fn().mockResolvedValue(undefined),
        read: jest.fn().mockResolvedValue('# Hello'),
      },
      metadataCache,
    };
  }

  function makeMockClient() {
    return {
      listFiles: jest.fn().mockResolvedValue([]),
      getNoteKey: jest.fn().mockResolvedValue(KEY),
      sync: jest.fn().mockResolvedValue({ commit_sha: 'abc1234567890', note_keys: {} }),
    };
  }

  function makeSettings() {
    return {
      repoMappings: { 'repo-1': { repoName: 'Test', vaultFolder: 'Published', subfolder: '' } },
      pullSHAs: {},
      syncHashes: {},
      noteKeys: {} as Record<string, Record<string, string>>,
    };
  }

  function getNotices(): string[] {
    return (jest.requireMock('obsidian') as { __notices: string[] }).__notices;
  }

  beforeEach(() => {
    (renderNoteToHTML as jest.Mock).mockReset();
    (renderNoteToHTML as jest.Mock).mockResolvedValue({ html: '<p>mock</p>', assets: new Map() });
    getNotices().length = 0;
  });

  test('a brand-new file with no metadata cache yet is retried and synced once Obsidian finishes indexing it', async () => {
    const file = makeMockFile('Published/notes/new.md');
    const metadataCache = makeMetadataCache(); // starts empty — file not yet indexed
    const app = makeMockApp([file], metadataCache);
    const client = makeMockClient();
    const settings = makeSettings();
    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));

    const syncPromise = manager.syncRepo('repo-1');

    // Flush microtasks until SyncManager has registered its 'resolve' listener
    // (i.e. it observed the missing cache and started waiting), then simulate
    // Obsidian finishing indexing the file.
    for (let i = 0; i < 50 && metadataCache.on.mock.calls.length === 0; i++) {
      await Promise.resolve();
    }
    expect(metadataCache.on.mock.calls.length).toBeGreaterThan(0);
    metadataCache.__emitResolve(file);

    await syncPromise;

    expect(client.sync).toHaveBeenCalled();
    const syncFiles = (client.sync as jest.Mock).mock.calls[0][1];
    expect(syncFiles).toHaveLength(1);
    expect(syncFiles[0].path).toBe('notes/new.md');
    expect(getNotices().some(n => n.includes('skipped'))).toBe(false);
  });

  test('one file failing to render is skipped with a Notice while the rest of the sync still completes', async () => {
    const goodFile = makeMockFile('Published/notes/good.md');
    const badFile = makeMockFile('Published/notes/bad.md');
    const metadataCache = makeMetadataCache(new Set([goodFile.path, badFile.path]));
    const app = makeMockApp([goodFile, badFile], metadataCache);
    const client = makeMockClient();
    const settings = makeSettings();

    (renderNoteToHTML as jest.Mock).mockImplementation((_app: unknown, _content: string, sourcePath: string) => {
      if (sourcePath === badFile.path) return Promise.reject(new Error('boom'));
      return Promise.resolve({ html: '<p>ok</p>', assets: new Map() });
    });

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));

    await expect(manager.syncRepo('repo-1')).resolves.toBeUndefined();

    expect(client.sync).toHaveBeenCalled();
    const syncFiles = (client.sync as jest.Mock).mock.calls[0][1];
    expect(syncFiles).toHaveLength(1);
    expect(syncFiles[0].path).toBe('notes/good.md');

    expect(getNotices().some(n => n.includes('bad') && n.includes('skipped') && n.includes('boom'))).toBe(true);
  });
});
