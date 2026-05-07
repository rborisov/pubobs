// Mock renderer to avoid DOM dependency (document.styleSheets)
jest.mock('../src/renderer', () => ({
  renderNoteToHTML: jest.fn().mockResolvedValue({ html: '<p>mock</p>', assets: new Map() }),
  extractStyles: jest.fn().mockReturnValue(''),
}));

// Mock obsidian before importing sync so the module loads without errors
jest.mock('obsidian', () => ({
  App: class {},
  TFile: class {},
  Notice: class { constructor() {} setMessage() {} hide() {} },
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
});
