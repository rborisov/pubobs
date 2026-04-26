# Phase 1 — PubObs Obsidian Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an Obsidian plugin that syncs vault subfolders to HTTPS Git remotes with per-folder configuration and environment validation.

**Architecture:** Four focused modules — `SettingsManager` (data + UI), `EnvironmentValidator` (workspace.json checks), `GitService` (isomorphic-git wrapper), `SyncOrchestrator` (wires them together) — wired in `main.ts`. Each folder in the vault is an independent Git working tree.

**Tech Stack:** TypeScript, isomorphic-git, @isomorphic-git/http-node, Obsidian Plugin API, Jest + ts-jest

---

## File Map

| File | Responsibility |
| --- | --- |
| `src/settings.ts` | `FolderRepo`, `PubObsSettings`, `DEFAULT_SETTINGS`, `SettingsManager`, `PubObsSettingTab` |
| `src/validator.ts` | `WorkspaceManifest`, `ValidationError`, `ValidationResult`, `EnvironmentValidator` |
| `src/git.ts` | `GitService` (clone, pull, stage, commit, push) |
| `src/orchestrator.ts` | `SyncOrchestrator` (resolves active folder, runs sync pipeline) |
| `src/main.ts` | Plugin entry point — registers commands, settings tab, auto-sync watcher |
| `tests/__mocks__/obsidian.ts` | Minimal Obsidian API stub for Jest |
| `tests/validator.test.ts` | Unit tests for `EnvironmentValidator` |
| `tests/git.test.ts` | Integration tests for `GitService` |
| `manifest.json` | Obsidian plugin manifest |
| `package.json` | Dependencies and scripts |
| `tsconfig.json` | TypeScript config for build |
| `tsconfig.test.json` | TypeScript config for tests (maps obsidian to mock) |
| `jest.config.js` | Jest config |
| `esbuild.config.mjs` | Esbuild bundler config |

---

## Task 0: Scaffold

**Files:**
- Create: `obsidian-plugin/manifest.json`
- Create: `obsidian-plugin/package.json`
- Create: `obsidian-plugin/tsconfig.json`
- Create: `obsidian-plugin/tsconfig.test.json`
- Create: `obsidian-plugin/jest.config.js`
- Create: `obsidian-plugin/esbuild.config.mjs`
- Create: `obsidian-plugin/tests/__mocks__/obsidian.ts`
- Create: `obsidian-plugin/src/settings.ts` (empty)
- Create: `obsidian-plugin/src/validator.ts` (empty)
- Create: `obsidian-plugin/src/git.ts` (empty)
- Create: `obsidian-plugin/src/orchestrator.ts` (empty)
- Create: `obsidian-plugin/src/main.ts` (empty)

- [ ] **Step 1: Create project directory and manifest**

```bash
mkdir -p obsidian-plugin/src obsidian-plugin/tests/__mocks__
```

`obsidian-plugin/manifest.json`:
```json
{
  "id": "pubobs",
  "name": "PubObs",
  "version": "0.1.0",
  "minAppVersion": "1.4.0",
  "description": "Collaborative publishing via Git",
  "author": "PubObs",
  "authorUrl": "",
  "isDesktopOnly": true
}
```

- [ ] **Step 2: Create package.json**

`obsidian-plugin/package.json`:
```json
{
  "name": "pubobs",
  "version": "0.1.0",
  "description": "PubObs Obsidian plugin",
  "main": "main.js",
  "scripts": {
    "dev": "node esbuild.config.mjs",
    "build": "tsc --noEmit -p tsconfig.json && node esbuild.config.mjs production",
    "test": "jest"
  },
  "license": "MIT",
  "devDependencies": {
    "@types/jest": "^29.5.0",
    "@types/node": "^20.0.0",
    "builtin-modules": "^3.3.0",
    "esbuild": "^0.20.0",
    "jest": "^29.7.0",
    "obsidian": "latest",
    "ts-jest": "^29.1.0",
    "typescript": "^5.3.0"
  },
  "dependencies": {
    "@isomorphic-git/http-node": "^1.0.2",
    "isomorphic-git": "^1.25.0"
  }
}
```

- [ ] **Step 3: Create tsconfig.json**

`obsidian-plugin/tsconfig.json`:
```json
{
  "compilerOptions": {
    "baseUrl": ".",
    "inlineSourceMap": true,
    "inlineSources": true,
    "module": "CommonJS",
    "target": "ES2018",
    "allowSyntheticDefaultImports": true,
    "esModuleInterop": true,
    "outDir": ".",
    "moduleResolution": "node",
    "isolatedModules": true,
    "strictNullChecks": true,
    "lib": ["ES2018", "DOM"],
    "types": ["node"]
  },
  "include": ["src/**/*.ts"],
  "exclude": ["node_modules"]
}
```

`obsidian-plugin/tsconfig.test.json`:
```json
{
  "extends": "./tsconfig.json",
  "compilerOptions": {
    "paths": {
      "obsidian": ["./tests/__mocks__/obsidian.ts"]
    }
  },
  "include": ["src/**/*.ts", "tests/**/*.ts"]
}
```

- [ ] **Step 4: Create jest.config.js**

`obsidian-plugin/jest.config.js`:
```js
module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  globals: {
    'ts-jest': {
      tsconfig: 'tsconfig.test.json',
    },
  },
  moduleNameMapper: {
    '^obsidian$': '<rootDir>/tests/__mocks__/obsidian.ts',
  },
  testMatch: ['**/tests/**/*.test.ts'],
};
```

- [ ] **Step 5: Create esbuild.config.mjs**

`obsidian-plugin/esbuild.config.mjs`:
```js
import esbuild from 'esbuild';
import process from 'process';
import builtins from 'builtin-modules';

const prod = process.argv[2] === 'production';

const context = await esbuild.context({
  entryPoints: ['src/main.ts'],
  bundle: true,
  external: [
    'obsidian',
    'electron',
    '@codemirror/autocomplete',
    '@codemirror/collab',
    '@codemirror/commands',
    '@codemirror/language',
    '@codemirror/lint',
    '@codemirror/search',
    '@codemirror/state',
    '@codemirror/view',
    '@lezer/common',
    '@lezer/highlight',
    '@lezer/lr',
    ...builtins,
  ],
  format: 'cjs',
  target: 'es2018',
  logLevel: 'info',
  sourcemap: prod ? false : 'inline',
  treeShaking: true,
  outfile: 'main.js',
});

if (prod) {
  await context.rebuild();
  process.exit(0);
} else {
  await context.watch();
}
```

- [ ] **Step 6: Create Obsidian mock**

`obsidian-plugin/tests/__mocks__/obsidian.ts`:
```ts
export interface App {
  vault: any;
  workspace: any;
  plugins: any;
  version: string;
}

export class TFile {
  path = '';
  name = '';
  extension = '';
  basename = '';
  parent: any = null;
}

export class Plugin {
  app: any;
  manifest: any;
  constructor(app: any, manifest: any) {
    this.app = app;
    this.manifest = manifest;
  }
  async loadData(): Promise<any> { return {}; }
  async saveData(_data: any): Promise<void> {}
  addCommand(_cmd: any): void {}
  addSettingTab(_tab: any): void {}
  registerEvent(_ref: any): void {}
}

export class PluginSettingTab {
  app: any;
  plugin: any;
  containerEl: any = {
    empty: () => {},
    createEl: (_tag: string, _opts?: any) => ({ setText: () => {}, createEl: () => ({}) }),
  };
  constructor(app: any, plugin: any) {
    this.app = app;
    this.plugin = plugin;
  }
  display(): void {}
  hide(): void {}
}

export class Setting {
  constructor(_el: any) {}
  setName(_n: string): this { return this; }
  setDesc(_d: string): this { return this; }
  addText(_cb: any): this { return this; }
  addToggle(_cb: any): this { return this; }
  addButton(_cb: any): this { return this; }
  addExtraButton(_cb: any): this { return this; }
}

export class Notice {
  constructor(_msg: string, _timeout?: number) {}
}

export function normalizePath(path: string): string {
  return path;
}
```

- [ ] **Step 7: Create empty source files**

```bash
touch obsidian-plugin/src/settings.ts
touch obsidian-plugin/src/validator.ts
touch obsidian-plugin/src/git.ts
touch obsidian-plugin/src/orchestrator.ts
touch obsidian-plugin/src/main.ts
```

- [ ] **Step 8: Install dependencies**

```bash
cd obsidian-plugin && npm install
```

Expected: `node_modules/` created, no errors.

- [ ] **Step 9: Commit scaffold**

```bash
git add obsidian-plugin/
git commit -m "feat: scaffold Phase 1 Obsidian plugin project"
```

---

## Task 1: EnvironmentValidator

**Files:**
- Create: `obsidian-plugin/tests/validator.test.ts`
- Modify: `obsidian-plugin/src/validator.ts`

- [ ] **Step 1: Write failing tests**

`obsidian-plugin/tests/validator.test.ts`:
```ts
import * as fsp from 'fs/promises';
import { EnvironmentValidator } from '../src/validator';

jest.mock('fs/promises');
const mockReadFile = fsp.readFile as jest.MockedFunction<typeof fsp.readFile>;

function makeApp(opts: {
  version?: string;
  plugins?: Record<string, { manifest: { version: string } }>;
} = {}): any {
  return {
    vault: { adapter: { basePath: '/vault' } },
    plugins: { plugins: opts.plugins ?? {} },
    version: opts.version ?? '1.4.0',
  };
}

const VALID_MANIFEST = {
  minObsidianVersion: '1.4.0',
  requiredPlugins: [{ id: 'dataview', minVersion: '0.5.55' }],
  snapshotFormat: '1.0',
};

describe('EnvironmentValidator', () => {
  beforeEach(() => jest.clearAllMocks());

  it('passes for a valid environment', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({
      plugins: { dataview: { manifest: { version: '0.5.55' } } },
    }));
    const result = await v.check('notes');
    expect(result.valid).toBe(true);
    expect(result.errors).toHaveLength(0);
  });

  it('returns missing-manifest error when workspace.json not found', async () => {
    mockReadFile.mockRejectedValue(Object.assign(new Error('ENOENT'), { code: 'ENOENT' }));
    const v = new EnvironmentValidator(makeApp());
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('missing-manifest');
    expect(result.errors[0].message).toContain('workspace.json');
  });

  it('returns obsidian-version error when app version is too old', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({
      version: '1.3.2',
      plugins: { dataview: { manifest: { version: '0.5.55' } } },
    }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('obsidian-version');
    expect(result.errors[0].message).toContain('1.3.2');
    expect(result.errors[0].message).toContain('1.4.0');
  });

  it('returns plugin-missing error when required plugin is not installed', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({ plugins: {} }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('plugin-missing');
    expect(result.errors[0].message).toContain('dataview');
  });

  it('returns plugin-version error when plugin version is too old', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({
      plugins: { dataview: { manifest: { version: '0.4.12' } } },
    }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('plugin-version');
    expect(result.errors[0].message).toContain('0.4.12');
    expect(result.errors[0].message).toContain('0.5.55');
  });

  it('reports all failures, not just the first', async () => {
    const manifest = {
      minObsidianVersion: '1.4.0',
      requiredPlugins: [
        { id: 'dataview', minVersion: '0.5.55' },
        { id: 'templater-obsidian', minVersion: '2.0.0' },
      ],
      snapshotFormat: '1.0',
    };
    mockReadFile.mockResolvedValue(JSON.stringify(manifest) as any);
    const v = new EnvironmentValidator(makeApp({ version: '1.3.0', plugins: {} }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    // version error + 2 missing plugin errors
    expect(result.errors.length).toBeGreaterThanOrEqual(3);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd obsidian-plugin && npx jest tests/validator.test.ts --no-coverage
```

Expected: all 6 tests FAIL with "Cannot find module '../src/validator'"

- [ ] **Step 3: Implement validator.ts**

`obsidian-plugin/src/validator.ts`:
```ts
import * as fsp from 'fs/promises';
import * as path from 'path';
import { App } from 'obsidian';

export interface WorkspaceManifest {
  minObsidianVersion: string;
  requiredPlugins: Array<{ id: string; minVersion: string }>;
  snapshotFormat: string;
}

export interface ValidationError {
  type: 'missing-manifest' | 'obsidian-version' | 'plugin-missing' | 'plugin-version';
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

export class EnvironmentValidator {
  constructor(private app: App) {}

  async check(folderPath: string): Promise<ValidationResult> {
    const vaultPath = (this.app.vault.adapter as any).basePath as string;
    const manifestPath = path.join(vaultPath, folderPath, 'workspace.json');

    let manifest: WorkspaceManifest;
    try {
      const raw = await fsp.readFile(manifestPath, 'utf-8');
      manifest = JSON.parse(raw) as WorkspaceManifest;
    } catch {
      return {
        valid: false,
        errors: [{
          type: 'missing-manifest',
          message: 'PubObs: workspace.json not found in repo root. Create it to enable sync.',
        }],
      };
    }

    const errors: ValidationError[] = [];
    const currentVersion = (this.app as any).version as string;

    if (!semverGte(currentVersion, manifest.minObsidianVersion)) {
      errors.push({
        type: 'obsidian-version',
        message: `PubObs: Obsidian ${manifest.minObsidianVersion}+ required. You have ${currentVersion}. Please upgrade before syncing.`,
      });
    }

    const installedPlugins = (this.app as any).plugins.plugins as Record<string, { manifest: { version: string } }>;
    for (const required of manifest.requiredPlugins ?? []) {
      const installed = installedPlugins[required.id];
      if (!installed) {
        errors.push({
          type: 'plugin-missing',
          message: `PubObs: Plugin '${required.id}' is required but not installed.`,
        });
        continue;
      }
      if (!semverGte(installed.manifest.version, required.minVersion)) {
        errors.push({
          type: 'plugin-version',
          message: `PubObs: Plugin '${required.id}' ${required.minVersion}+ required. Installed: ${installed.manifest.version}. Please upgrade.`,
        });
      }
    }

    return { valid: errors.length === 0, errors };
  }
}

function semverGte(a: string, b: string): boolean {
  const pa = a.split('.').map(Number);
  const pb = b.split('.').map(Number);
  for (let i = 0; i < 3; i++) {
    if ((pa[i] ?? 0) > (pb[i] ?? 0)) return true;
    if ((pa[i] ?? 0) < (pb[i] ?? 0)) return false;
  }
  return true;
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd obsidian-plugin && npx jest tests/validator.test.ts --no-coverage
```

Expected: all 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add obsidian-plugin/src/validator.ts obsidian-plugin/tests/validator.test.ts
git commit -m "feat: add EnvironmentValidator with TDD"
```

---

## Task 2: GitService

**Files:**
- Create: `obsidian-plugin/tests/git.test.ts`
- Modify: `obsidian-plugin/src/git.ts`

- [ ] **Step 1: Write failing tests**

`obsidian-plugin/tests/git.test.ts`:
```ts
import * as os from 'os';
import * as path from 'path';
import * as fsp from 'fs/promises';
import * as fs from 'fs';
import git from 'isomorphic-git';

// Mock network operations; keep all local filesystem operations real
jest.mock('isomorphic-git', () => ({
  ...jest.requireActual('isomorphic-git'),
  clone: jest.fn().mockResolvedValue({}),
  pull: jest.fn().mockResolvedValue({}),
  push: jest.fn().mockResolvedValue({ ok: true }),
}));

import { GitService } from '../src/git';
import { PubObsSettings, FolderRepo } from '../src/settings';

const SETTINGS: PubObsSettings = {
  defaultUsername: 'testuser',
  defaultPat: 'testpat',
  defaultBranch: 'main',
  autoSync: false,
  repos: [],
};

async function makeTestRepo(): Promise<{
  vaultPath: string;
  folderPath: string;
  repo: FolderRepo;
  service: GitService;
  cleanup: () => Promise<void>;
}> {
  const vaultPath = await fsp.mkdtemp(path.join(os.tmpdir(), 'pubobs-test-'));
  const folderPath = 'notes';
  const dir = path.join(vaultPath, folderPath);
  await fsp.mkdir(dir, { recursive: true });

  await git.init({ fs, dir, defaultBranch: 'main' });
  await fsp.writeFile(path.join(dir, 'README.md'), '# Test');
  await git.add({ fs, dir, filepath: 'README.md' });
  await git.commit({
    fs,
    dir,
    message: 'initial',
    author: { name: 'test', email: 'test@test.com' },
  });

  const repo: FolderRepo = { folderPath, remoteUrl: 'https://example.com/test.git' };
  const service = new GitService(vaultPath, () => SETTINGS);
  const cleanup = () => fsp.rm(vaultPath, { recursive: true, force: true });
  return { vaultPath, folderPath, repo, service, cleanup };
}

describe('GitService', () => {
  beforeEach(() => jest.clearAllMocks());

  it('clone() calls git.clone with the correct dir and url', async () => {
    const mockGit = require('isomorphic-git') as jest.Mocked<typeof import('isomorphic-git')>;
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    await service.clone(repo);

    expect(mockGit.clone).toHaveBeenCalledWith(expect.objectContaining({
      url: 'https://example.com/repo.git',
      dir: path.join('/vault', 'notes'),
    }));
  });

  it('pull() calls git.pull with the correct dir', async () => {
    const mockGit = require('isomorphic-git') as jest.Mocked<typeof import('isomorphic-git')>;
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    await service.pull(repo);

    expect(mockGit.pull).toHaveBeenCalledWith(expect.objectContaining({
      dir: path.join('/vault', 'notes'),
    }));
  });

  it('stage() stages modified and new files, returns their paths', async () => {
    const { vaultPath, folderPath, repo, service, cleanup } = await makeTestRepo();
    try {
      await fsp.writeFile(path.join(vaultPath, folderPath, 'README.md'), '# Modified');
      await fsp.writeFile(path.join(vaultPath, folderPath, 'note.md'), '# New note');

      const staged = await service.stage(repo);

      expect(staged).toContain('README.md');
      expect(staged).toContain('note.md');
    } finally {
      await cleanup();
    }
  });

  it('commit() creates a commit with the given message and returns the sha', async () => {
    const { vaultPath, folderPath, repo, service, cleanup } = await makeTestRepo();
    try {
      await fsp.writeFile(path.join(vaultPath, folderPath, 'note.md'), '# Note');
      await service.stage(repo);

      const sha = await service.commit(repo, 'pubobs: sync 2026-04-26T12:00:00.000Z');

      expect(sha).toBeTruthy();
      const log = await git.log({ fs, dir: path.join(vaultPath, folderPath) });
      expect(log[0].commit.message.trim()).toBe('pubobs: sync 2026-04-26T12:00:00.000Z');
    } finally {
      await cleanup();
    }
  });

  it('commit() returns null and creates no commit when nothing is staged', async () => {
    const { vaultPath, folderPath, repo, service, cleanup } = await makeTestRepo();
    try {
      const logBefore = await git.log({ fs, dir: path.join(vaultPath, folderPath) });

      const sha = await service.commit(repo, 'pubobs: sync 2026-04-26T12:00:00.000Z');

      expect(sha).toBeNull();
      const logAfter = await git.log({ fs, dir: path.join(vaultPath, folderPath) });
      expect(logAfter).toHaveLength(logBefore.length);
    } finally {
      await cleanup();
    }
  });

  it('push() calls git.push with the correct dir', async () => {
    const mockGit = require('isomorphic-git') as jest.Mocked<typeof import('isomorphic-git')>;
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    await service.push(repo);

    expect(mockGit.push).toHaveBeenCalledWith(expect.objectContaining({
      dir: path.join('/vault', 'notes'),
    }));
  });

  it('testConnection() returns ok:true when getRemoteInfo2 succeeds', async () => {
    const mockGit = require('isomorphic-git') as any;
    mockGit.getRemoteInfo2 = jest.fn().mockResolvedValue({ capabilities: [] });
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    const result = await service.testConnection(repo);

    expect(result.ok).toBe(true);
  });

  it('testConnection() returns ok:false when getRemoteInfo2 throws', async () => {
    const mockGit = require('isomorphic-git') as any;
    mockGit.getRemoteInfo2 = jest.fn().mockRejectedValue(new Error('403 Forbidden'));
    const service = new GitService('/vault', () => SETTINGS);
    const repo: FolderRepo = { folderPath: 'notes', remoteUrl: 'https://example.com/repo.git' };

    const result = await service.testConnection(repo);

    expect(result.ok).toBe(false);
    expect(result.message).toContain('403');
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd obsidian-plugin && npx jest tests/git.test.ts --no-coverage
```

Expected: all 8 tests FAIL with "Cannot find module '../src/git'"

- [ ] **Step 3: Add FolderRepo and PubObsSettings types to settings.ts** (required by git.ts before it compiles)

`obsidian-plugin/src/settings.ts` (types only for now — SettingsManager added in Task 3):
```ts
export interface FolderRepo {
  folderPath: string;
  remoteUrl: string;
  username?: string;
  pat?: string;
  branch?: string;
}

export interface PubObsSettings {
  defaultUsername: string;
  defaultPat: string;
  defaultBranch: string;
  autoSync: boolean;
  repos: FolderRepo[];
}

export const DEFAULT_SETTINGS: PubObsSettings = {
  defaultUsername: '',
  defaultPat: '',
  defaultBranch: 'main',
  autoSync: false,
  repos: [],
};
```

- [ ] **Step 4: Implement git.ts**

`obsidian-plugin/src/git.ts`:
```ts
import git from 'isomorphic-git';
import http from '@isomorphic-git/http-node';
import * as fs from 'fs';
import * as fsp from 'fs/promises';
import * as path from 'path';
import { FolderRepo, PubObsSettings } from './settings';

export class GitService {
  constructor(
    private vaultPath: string,
    private getSettings: () => PubObsSettings,
    private fsImpl: typeof fs = fs,
    private httpImpl: typeof http = http,
  ) {}

  private get settings(): PubObsSettings { return this.getSettings(); }

  private repoDir(repo: FolderRepo): string {
    return path.join(this.vaultPath, repo.folderPath);
  }

  private resolveAuth(repo: FolderRepo): { username: string; password: string } {
    return {
      username: repo.username ?? this.settings.defaultUsername,
      password: repo.pat ?? this.settings.defaultPat,
    };
  }

  private resolveBranch(repo: FolderRepo): string {
    return repo.branch ?? this.settings.defaultBranch;
  }

  async clone(repo: FolderRepo): Promise<void> {
    const { username, password } = this.resolveAuth(repo);
    const dir = this.repoDir(repo);
    await fsp.mkdir(dir, { recursive: true });
    await git.clone({
      fs: this.fsImpl,
      http: this.httpImpl,
      dir,
      url: repo.remoteUrl,
      ref: this.resolveBranch(repo),
      singleBranch: true,
      onAuth: () => ({ username, password }),
    });
  }

  async pull(repo: FolderRepo): Promise<void> {
    const { username, password } = this.resolveAuth(repo);
    await git.pull({
      fs: this.fsImpl,
      http: this.httpImpl,
      dir: this.repoDir(repo),
      ref: this.resolveBranch(repo),
      singleBranch: true,
      onAuth: () => ({ username, password }),
      author: { name: username || 'PubObs', email: username || 'pubobs@local' },
    });
  }

  async stage(repo: FolderRepo): Promise<string[]> {
    const dir = this.repoDir(repo);
    const statusMatrix = await git.statusMatrix({ fs: this.fsImpl, dir });
    const staged: string[] = [];
    for (const [filepath, head, workdir] of statusMatrix) {
      if (head !== workdir) {
        if (workdir === 0) {
          await git.remove({ fs: this.fsImpl, dir, filepath: filepath as string });
        } else {
          await git.add({ fs: this.fsImpl, dir, filepath: filepath as string });
        }
        staged.push(filepath as string);
      }
    }
    return staged;
  }

  async commit(repo: FolderRepo, message: string): Promise<string | null> {
    const dir = this.repoDir(repo);
    const statusMatrix = await git.statusMatrix({ fs: this.fsImpl, dir });
    const hasStagedChanges = statusMatrix.some(([, head, , stage]) => head !== stage);
    if (!hasStagedChanges) return null;

    const { username } = this.resolveAuth(repo);
    return git.commit({
      fs: this.fsImpl,
      dir,
      message,
      author: { name: username || 'PubObs', email: username || 'pubobs@local' },
    });
  }

  async push(repo: FolderRepo): Promise<void> {
    const { username, password } = this.resolveAuth(repo);
    await git.push({
      fs: this.fsImpl,
      http: this.httpImpl,
      dir: this.repoDir(repo),
      ref: this.resolveBranch(repo),
      onAuth: () => ({ username, password }),
    });
  }

  async testConnection(repo: FolderRepo): Promise<{ ok: boolean; message: string }> {
    const { username, password } = this.resolveAuth(repo);
    try {
      await git.getRemoteInfo2({
        http: this.httpImpl,
        url: repo.remoteUrl,
        onAuth: () => ({ username, password }),
      });
      return { ok: true, message: 'Connected' };
    } catch (e) {
      return { ok: false, message: (e as Error).message };
    }
  }
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd obsidian-plugin && npx jest tests/git.test.ts --no-coverage
```

Expected: all 8 tests PASS

- [ ] **Step 6: Run the full test suite**

```bash
cd obsidian-plugin && npx jest --no-coverage
```

Expected: all 14 tests PASS

- [ ] **Step 7: Commit**

```bash
git add obsidian-plugin/src/settings.ts obsidian-plugin/src/git.ts obsidian-plugin/tests/git.test.ts
git commit -m "feat: add GitService (isomorphic-git wrapper) with TDD"
```

---

## Task 3: SettingsManager

**Files:**
- Modify: `obsidian-plugin/src/settings.ts`

- [ ] **Step 1: Add SettingsManager class to settings.ts**

Append to the existing content of `obsidian-plugin/src/settings.ts`:
```ts
import { Plugin } from 'obsidian';

export class SettingsManager {
  settings: PubObsSettings = { ...DEFAULT_SETTINGS, repos: [] };

  constructor(private plugin: Plugin) {}

  async load(): Promise<void> {
    const saved = await this.plugin.loadData();
    Object.assign(this.settings, DEFAULT_SETTINGS, saved ?? {});
  }

  async save(): Promise<void> {
    await this.plugin.saveData(this.settings);
  }

  async update(changes: Partial<PubObsSettings>): Promise<void> {
    Object.assign(this.settings, changes);
    await this.save();
  }
}
```

Note: `Object.assign(this.settings, ...)` mutates the existing settings object in place so any references held by other modules (e.g. `() => settingsManager.settings`) always reflect the latest state.

- [ ] **Step 2: Run the full test suite to confirm nothing broke**

```bash
cd obsidian-plugin && npx jest --no-coverage
```

Expected: all 14 tests PASS

- [ ] **Step 3: Commit**

```bash
git add obsidian-plugin/src/settings.ts
git commit -m "feat: add SettingsManager"
```

---

## Task 4: SyncOrchestrator

**Files:**
- Modify: `obsidian-plugin/src/orchestrator.ts`

- [ ] **Step 1: Implement SyncOrchestrator**

`obsidian-plugin/src/orchestrator.ts`:
```ts
import { App, Notice } from 'obsidian';
import { SettingsManager, FolderRepo } from './settings';
import { EnvironmentValidator } from './validator';
import { GitService } from './git';

export class SyncOrchestrator {
  private validator: EnvironmentValidator;
  private git: GitService;

  constructor(
    private app: App,
    private settingsManager: SettingsManager,
    private vaultPath: string,
  ) {
    this.validator = new EnvironmentValidator(app);
    this.git = new GitService(vaultPath, () => settingsManager.settings);
  }

  resolveFolderForFile(filePath: string): FolderRepo | undefined {
    return this.settingsManager.settings.repos.find(
      repo => filePath === repo.folderPath || filePath.startsWith(repo.folderPath + '/'),
    );
  }

  async cloneFolder(repo: FolderRepo): Promise<void> {
    await this.git.clone(repo);
  }

  async testFolderConnection(repo: FolderRepo): Promise<{ ok: boolean; message: string }> {
    return this.git.testConnection(repo);
  }

  async syncCurrentFile(): Promise<void> {
    const activeFile = this.app.workspace.getActiveFile();
    if (!activeFile) {
      new Notice('PubObs: No active file.');
      return;
    }
    const repo = this.resolveFolderForFile(activeFile.path);
    if (!repo) {
      new Notice('PubObs: This file is not in a linked PubObs folder.');
      return;
    }
    await this.syncFolder(repo);
  }

  async syncFolder(repo: FolderRepo): Promise<void> {
    const validation = await this.validator.check(repo.folderPath);
    if (!validation.valid) {
      for (const error of validation.errors) {
        new Notice(error.message, 8000);
      }
      return;
    }

    try {
      new Notice(`PubObs: Syncing ${repo.folderPath}…`);
      await this.git.pull(repo);
      const staged = await this.git.stage(repo);
      const timestamp = new Date().toISOString();
      const sha = await this.git.commit(repo, `pubobs: sync ${timestamp}`);
      if (!sha) {
        new Notice('PubObs: Nothing to sync.');
        return;
      }
      await this.git.push(repo);
      new Notice(`PubObs: Synced ${staged.length} file(s) — ${sha.slice(0, 7)}`);
    } catch (e) {
      const msg = (e as Error).message ?? String(e);
      if (/401|403|auth/i.test(msg)) {
        new Notice(
          'PubObs: Push failed — authentication error. Check your token in PubObs settings.',
          10000,
        );
      } else {
        new Notice(`PubObs: Sync failed — ${msg}`, 8000);
      }
    }
  }
}
```

- [ ] **Step 2: Run the full test suite**

```bash
cd obsidian-plugin && npx jest --no-coverage
```

Expected: all 14 tests PASS

- [ ] **Step 3: Commit**

```bash
git add obsidian-plugin/src/orchestrator.ts
git commit -m "feat: add SyncOrchestrator"
```

---

## Task 5: Settings UI (SettingsTab)

**Files:**
- Modify: `obsidian-plugin/src/settings.ts`

- [ ] **Step 1: Add PubObsSettingTab class to settings.ts**

First, update the import line at the top of `obsidian-plugin/src/settings.ts` — replace `import { Plugin } from 'obsidian'` with:

```ts
import { Plugin, App, PluginSettingTab, Setting, Notice } from 'obsidian';
```

Then append the following to the bottom of `obsidian-plugin/src/settings.ts`:
```ts
// Minimal interface for the plugin reference — avoids circular import with main.ts
interface PluginRef {
  settingsManager: SettingsManager;
  orchestrator: {
    cloneFolder(repo: FolderRepo): Promise<void>;
    testFolderConnection(repo: FolderRepo): Promise<{ ok: boolean; message: string }>;
  };
}

export class PubObsSettingTab extends PluginSettingTab {
  constructor(app: App, private plugin: PluginRef & any) {
    super(app, plugin);
  }

  display(): void {
    const { containerEl } = this;
    containerEl.empty();

    containerEl.createEl('h2', { text: 'PubObs' });

    // ── Default Credentials ──────────────────────────────────────────────
    containerEl.createEl('h3', { text: 'Default Credentials' });

    new Setting(containerEl)
      .setName('Username')
      .addText(text => text
        .setValue(this.plugin.settingsManager.settings.defaultUsername)
        .onChange(async v => this.plugin.settingsManager.update({ defaultUsername: v })));

    new Setting(containerEl)
      .setName('Personal Access Token')
      .addText(text => {
        text.inputEl.type = 'password';
        text.setValue(this.plugin.settingsManager.settings.defaultPat)
          .onChange(async v => this.plugin.settingsManager.update({ defaultPat: v }));
      });

    new Setting(containerEl)
      .setName('Default branch')
      .addText(text => text
        .setValue(this.plugin.settingsManager.settings.defaultBranch)
        .onChange(async v => this.plugin.settingsManager.update({ defaultBranch: v })));

    // ── Linked Folders ───────────────────────────────────────────────────
    containerEl.createEl('h3', { text: 'Linked Folders' });

    const repos = this.plugin.settingsManager.settings.repos;
    if (repos.length === 0) {
      containerEl.createEl('p', {
        text: 'No folders linked yet.',
        cls: 'setting-item-description',
      });
    }
    for (const repo of repos) {
      this.renderRepoRow(containerEl, repo);
    }

    // Add-folder form (always visible at bottom of linked folders list)
    containerEl.createEl('h4', { text: 'Add folder' });
    let newFolderPath = '';
    let newRemoteUrl = '';

    new Setting(containerEl)
      .setName('Folder path')
      .setDesc('Relative to vault root, e.g. project-a')
      .addText(text => text
        .setPlaceholder('project-a')
        .onChange(v => { newFolderPath = v; }));

    new Setting(containerEl)
      .setName('Remote URL')
      .addText(text => text
        .setPlaceholder('https://gogs.example.com/team/project-a.git')
        .onChange(v => { newRemoteUrl = v; }));

    new Setting(containerEl)
      .setName('Clone remote into folder')
      .addButton(btn => btn
        .setButtonText('Clone / Initialize')
        .onClick(async () => {
          if (!newFolderPath || !newRemoteUrl) return;
          const repo: FolderRepo = { folderPath: newFolderPath, remoteUrl: newRemoteUrl };
          try {
            await this.plugin.orchestrator.cloneFolder(repo);
            const repos = [...this.plugin.settingsManager.settings.repos, repo];
            await this.plugin.settingsManager.update({ repos });
            this.display();
          } catch (e) {
            new Notice(`PubObs: Clone failed — ${(e as Error).message}`, 8000);
          }
        }));

    // ── Sync ─────────────────────────────────────────────────────────────
    containerEl.createEl('h3', { text: 'Sync' });

    new Setting(containerEl)
      .setName('Auto-sync on save')
      .setDesc('Commit and push every time a note in a linked folder is saved (requires plugin reload)')
      .addToggle(toggle => toggle
        .setValue(this.plugin.settingsManager.settings.autoSync)
        .onChange(async v => this.plugin.settingsManager.update({ autoSync: v })));
  }

  private renderRepoRow(containerEl: HTMLElement, repo: FolderRepo): void {
    new Setting(containerEl)
      .setName(repo.folderPath)
      .setDesc(repo.remoteUrl)
      .addButton(btn => btn
        .setButtonText('Test connection')
        .onClick(async () => {
          const result = await this.plugin.orchestrator.testFolderConnection(repo);
          new Notice(
            result.ok
              ? `PubObs: ${repo.folderPath} — Connected ✓`
              : `PubObs: ${repo.folderPath} — Failed ✗ ${result.message}`,
            5000,
          );
        }))
      .addButton(btn => btn
        .setButtonText('Remove')
        .onClick(async () => {
          const repos = this.plugin.settingsManager.settings.repos
            .filter(r => r.folderPath !== repo.folderPath);
          await this.plugin.settingsManager.update({ repos });
          this.display();
        }));
  }
}
```

- [ ] **Step 2: Run the full test suite**

```bash
cd obsidian-plugin && npx jest --no-coverage
```

Expected: all 14 tests PASS

- [ ] **Step 3: Commit**

```bash
git add obsidian-plugin/src/settings.ts
git commit -m "feat: add PubObsSettingTab UI"
```

---

## Task 6: Plugin Entry Point

**Files:**
- Modify: `obsidian-plugin/src/main.ts`

- [ ] **Step 1: Implement main.ts**

`obsidian-plugin/src/main.ts`:
```ts
import { Plugin } from 'obsidian';
import { SettingsManager, PubObsSettingTab } from './settings';
import { SyncOrchestrator } from './orchestrator';

export default class PubObsPlugin extends Plugin {
  settingsManager!: SettingsManager;
  orchestrator!: SyncOrchestrator;

  async onload(): Promise<void> {
    this.settingsManager = new SettingsManager(this);
    await this.settingsManager.load();

    const vaultPath = (this.app.vault.adapter as any).basePath as string;
    this.orchestrator = new SyncOrchestrator(this.app, this.settingsManager, vaultPath);

    this.addCommand({
      id: 'sync-current-folder',
      name: 'Sync current folder',
      callback: () => { void this.orchestrator.syncCurrentFile(); },
    });

    this.addSettingTab(new PubObsSettingTab(this.app, this));

    if (this.settingsManager.settings.autoSync) {
      this.registerEvent(
        this.app.vault.on('modify', file => {
          const repo = this.orchestrator.resolveFolderForFile(file.path);
          if (repo) void this.orchestrator.syncFolder(repo);
        }),
      );
    }
  }

  async onunload(): Promise<void> {}
}
```

- [ ] **Step 2: Run the full test suite one final time**

```bash
cd obsidian-plugin && npx jest --no-coverage
```

Expected: all 14 tests PASS

- [ ] **Step 3: Commit**

```bash
git add obsidian-plugin/src/main.ts
git commit -m "feat: add plugin entry point, wire all modules"
```

---

## Task 7: Build Verification

**Files:** none new — verifies the build compiles and bundles without errors

- [ ] **Step 1: Run TypeScript type check**

```bash
cd obsidian-plugin && npx tsc --noEmit -p tsconfig.json
```

Expected: exits 0 with no errors

- [ ] **Step 2: Run esbuild production bundle**

```bash
cd obsidian-plugin && node esbuild.config.mjs production
```

Expected: `main.js` created in `obsidian-plugin/`, no errors

- [ ] **Step 3: Verify bundle size is reasonable**

```bash
ls -lh obsidian-plugin/main.js
```

Expected: file exists, size under 2 MB (isomorphic-git is ~500 KB bundled)

- [ ] **Step 4: Commit**

```bash
git add obsidian-plugin/main.js
git commit -m "build: production bundle for Phase 1 plugin"
```

---

## Manual Test Checklist

After installing the plugin into a real Obsidian vault (`obsidian-plugin/` copied to `.obsidian/plugins/pubobs/`):

- [ ] Settings tab renders correctly in Obsidian → Settings → PubObs
- [ ] Entering credentials and clicking "Clone / Initialize" clones a Gogs repo into a vault subfolder
- [ ] "Sync current folder" command appears in command palette and runs
- [ ] With a valid workspace.json, sync pushes a commit and shows green Notice with sha
- [ ] With Obsidian version below `minObsidianVersion`, sync shows red Notice and aborts
- [ ] With a missing plugin, sync shows red Notice naming the plugin
- [ ] With wrong PAT, sync shows "authentication error" Notice
- [ ] Auto-sync toggle enabled → saving a file in a linked folder triggers sync
- [ ] End-to-end test against a real Gogs instance
