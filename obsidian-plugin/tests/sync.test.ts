// Mock obsidian before importing sync so the module loads without errors
jest.mock('obsidian', () => ({
  App: class {},
  TFile: class {},
  Notice: class { constructor() {} setMessage() {} hide() {} },
}));

import { repoPathToVaultPath, SyncManager } from '../src/sync';

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

describe('SyncManager.pullRepo', () => {
  function makeMockApp(vaultFiles: Record<string, boolean> = {}) {
    const TFileMock = (jest.requireMock('obsidian') as any).TFile;
    return {
      vault: {
        getAbstractFileByPath: jest.fn((path: string) => {
          if (vaultFiles[path]) return new TFileMock();
          return null;
        }),
        create: jest.fn().mockResolvedValue(undefined),
        modify: jest.fn().mockResolvedValue(undefined),
        createFolder: jest.fn().mockResolvedValue(undefined),
      },
    };
  }

  function makeMockClient(files: Array<{ path: string; content: string; sha: string }>) {
    return { listFiles: jest.fn().mockResolvedValue(files) };
  }

  function makeSettings(pullSHAs: Record<string, Record<string, string>> = {}) {
    return {
      repoMappings: { 'repo-1': { repoName: 'Test', vaultFolder: 'Published', subfolder: '' } },
      pullSHAs,
    };
  }

  test('creates new file and updates SHA when file is new', async () => {
    const app = makeMockApp();
    const client = makeMockClient([{ path: 'notes/foo.md', content: '# Foo', sha: 'abc' }]);
    const settings = makeSettings();
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.pullRepo('repo-1');

    expect(app.vault.create).toHaveBeenCalledWith('Published/notes/foo.md', '# Foo');
    expect(settings.pullSHAs['repo-1']['notes/foo.md']).toBe('abc');
    expect(save).toHaveBeenCalledTimes(1);
  });

  test('modifies existing file when SHA differs', async () => {
    const app = makeMockApp({ 'Published/notes/foo.md': true });
    const client = makeMockClient([{ path: 'notes/foo.md', content: '# Updated', sha: 'new-sha' }]);
    const settings = makeSettings({ 'repo-1': { 'notes/foo.md': 'old-sha' } });
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.pullRepo('repo-1');

    expect(app.vault.modify).toHaveBeenCalled();
    expect(app.vault.create).not.toHaveBeenCalled();
    expect(settings.pullSHAs['repo-1']['notes/foo.md']).toBe('new-sha');
  });

  test('skips file when SHA matches stored SHA', async () => {
    const app = makeMockApp();
    const client = makeMockClient([{ path: 'notes/foo.md', content: '# Foo', sha: 'abc' }]);
    const settings = makeSettings({ 'repo-1': { 'notes/foo.md': 'abc' } });
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.pullRepo('repo-1');

    expect(app.vault.create).not.toHaveBeenCalled();
    expect(app.vault.modify).not.toHaveBeenCalled();
    expect(save).toHaveBeenCalledTimes(1); // still saves (no-op update)
  });

  test('does not mutate settings.pullSHAs until after all writes', async () => {
    const app = makeMockApp();
    const client = makeMockClient([{ path: 'notes/foo.md', content: '# Foo', sha: 'abc' }]);
    const original: Record<string, string> = {};
    const settings = makeSettings({ 'repo-1': original });
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    // original object should not be mutated during the run
    await manager.pullRepo('repo-1');

    // The original object reference was not directly mutated (shallow copy was used)
    expect(original['notes/foo.md']).toBeUndefined();
    // But settings.pullSHAs was updated via assignment
    expect(settings.pullSHAs['repo-1']['notes/foo.md']).toBe('abc');
  });

  test('throws when no repo mapping exists', async () => {
    const app = makeMockApp();
    const client = makeMockClient([]);
    const settings = { repoMappings: {}, pullSHAs: {} };
    const save = jest.fn();
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await expect(manager.pullRepo('missing-repo')).rejects.toThrow('No folder mapping');
  });

  test('filters out non-.md files and _pubobs/ files', async () => {
    const app = makeMockApp();
    const client = makeMockClient([
      { path: '_pubobs/obsidian.css', content: 'body{}', sha: 'css-sha' },
      { path: 'notes/real.md', content: '# Real', sha: 'md-sha' },
    ]);
    const settings = makeSettings();
    const save = jest.fn().mockResolvedValue(undefined);
    const manager = new SyncManager(app as any, client as any, settings as any, save);

    await manager.pullRepo('repo-1');

    // Only the .md file should be written
    expect(app.vault.create).toHaveBeenCalledTimes(1);
    expect(app.vault.create).toHaveBeenCalledWith('Published/notes/real.md', '# Real');
  });
});
