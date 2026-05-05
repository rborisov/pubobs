# Phase 3 — PubObs Obsidian Plugin (Clean Rewrite)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete rewrite of the Obsidian plugin. No isomorphic-git — all git operations live on the backend. Plugin responsibilities: PKCE auth, vault file sync via `POST /api/repos/{id}/sync`, folder-to-repo mapping, and a settings UI.

**Architecture:** TypeScript, Obsidian API (`requestUrl`, `registerObsidianProtocolHandler`), Web Crypto for PKCE, esbuild + Jest. No external runtime dependencies.

**Auth flow summary:**
1. Plugin generates PKCE `verifier` + `challenge`, opens browser to `GET /auth/plugin?code_challenge=…&redirect_uri=obsidian://pubobs/callback&state=…`
2. Backend redirects through OIDC → redirects back to `obsidian://pubobs/callback?code=…&state=…`
3. Plugin exchanges `POST /auth/token {code, code_verifier}` → `{access_token, refresh_token, expires_in}`
4. BackendClient auto-refreshes via `POST /auth/refresh {refresh_token}` when token is near expiry

**Sync flow summary:**
1. User configures backend URL + maps vault folder → repo in settings tab
2. Sync command reads vault files under mapped folder, collects markdown content + frontmatter
3. `POST /api/repos/{id}/sync {files: [{path, md_content, html_content: "", frontmatter}]}`

---

## Parts overview (execute one per session)

| Part | Tasks | What it produces |
|------|-------|-----------------|
| **A** | 0–2 | Clean slate, build tooling, shared types, PKCE utilities |
| **B** | 3–4 | BackendClient with auto token refresh + unit tests |
| **C** | 5–6 | Auth flow: PKCE session + Obsidian protocol handler |
| **D** | 7–9 | Settings tab, sync command, main plugin entry point |

---

## Part A — Clean slate + types + PKCE

### Task 0: Remove old source files

The existing plugin (`git.ts`, `orchestrator.ts`, `validator.ts`, `settings.ts`) is incompatible with the new design. Delete these files before writing new ones.

**Files to delete:**
- `obsidian-plugin/src/git.ts`
- `obsidian-plugin/src/orchestrator.ts`
- `obsidian-plugin/src/validator.ts`
- `obsidian-plugin/src/settings.ts`
- `obsidian-plugin/src/main.ts`
- `obsidian-plugin/tests/git.test.ts`
- `obsidian-plugin/tests/validator.test.ts`

- [ ] **Step 1: Delete old files**

```bash
cd obsidian-plugin
rm -f src/git.ts src/orchestrator.ts src/validator.ts src/settings.ts src/main.ts
rm -f tests/git.test.ts tests/validator.test.ts
```

- [ ] **Step 2: Update package.json** — remove `isomorphic-git` dependency, keep all dev deps

`obsidian-plugin/package.json`:
```json
{
  "name": "pubobs",
  "version": "0.2.0",
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
    "esbuild": "^0.25.0",
    "jest": "^29.7.0",
    "jest-environment-node": "^29.7.0",
    "obsidian": "latest",
    "ts-jest": "^29.1.0",
    "typescript": "^5.3.0"
  }
}
```

- [ ] **Step 3: Run `npm install`** to remove isomorphic-git from node_modules

```bash
cd obsidian-plugin && npm install
```

---

### Task 1: Shared types

**File:** `obsidian-plugin/src/types.ts`

```typescript
export interface PubObsSettings {
  backendUrl: string;
  accessToken: string;
  refreshToken: string;
  tokenExpiresAt: number; // Unix seconds; 0 = not set
  repoMappings: Record<string, RepoMapping>; // repoId → mapping
}

export interface RepoMapping {
  repoName: string;   // display name, fetched from /api/repos
  vaultFolder: string; // absolute vault path (e.g. "Notes/Published")
  subfolder: string;   // path prefix within repo (e.g. "" or "posts/")
}

export interface RepoInfo {
  id: string;
  name: string;
  remote_url: string;
  default_branch: string;
}

export interface TokenResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number; // seconds
}

export const DEFAULT_SETTINGS: PubObsSettings = {
  backendUrl: '',
  accessToken: '',
  refreshToken: '',
  tokenExpiresAt: 0,
  repoMappings: {},
};
```

- [ ] **Step 1: Write `src/types.ts`** with the content above.

---

### Task 2: PKCE utilities

PKCE uses SHA-256 via Web Crypto (`crypto.subtle`), which is available in Electron/Node. Output is base64url without padding.

**File:** `obsidian-plugin/src/pkce.ts`

```typescript
export function generateVerifier(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return base64url(bytes);
}

export async function computeChallenge(verifier: string): Promise<string> {
  const data = new TextEncoder().encode(verifier);
  const hash = await crypto.subtle.digest('SHA-256', data);
  return base64url(new Uint8Array(hash));
}

export function generateState(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return base64url(bytes);
}

function base64url(bytes: Uint8Array): string {
  let str = '';
  for (const b of bytes) str += String.fromCharCode(b);
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}
```

**File:** `obsidian-plugin/tests/pkce.test.ts`

```typescript
import { generateVerifier, computeChallenge, generateState } from '../src/pkce';

describe('pkce', () => {
  test('generateVerifier returns 43-char base64url string', () => {
    const v = generateVerifier();
    expect(v).toMatch(/^[A-Za-z0-9\-_]+$/);
    expect(v.length).toBe(43);
  });

  test('computeChallenge returns base64url SHA-256 of verifier', async () => {
    // RFC 7636 test vector
    const verifier = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
    const challenge = await computeChallenge(verifier);
    expect(challenge).toBe('E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM');
  });

  test('generateState returns non-empty base64url string', () => {
    const s = generateState();
    expect(s.length).toBeGreaterThan(0);
    expect(s).toMatch(/^[A-Za-z0-9\-_]+$/);
  });

  test('two calls produce different verifiers', () => {
    expect(generateVerifier()).not.toBe(generateVerifier());
  });
});
```

- [ ] **Step 1: Write `src/pkce.ts`** with the content above.
- [ ] **Step 2: Write `tests/pkce.test.ts`** with the content above.
- [ ] **Step 3: Verify** — `npm test` — all 4 pkce tests pass.

> **Note on Jest config:** Jest needs `testEnvironment: 'node'` and the `crypto.subtle` global. If `crypto` is not available in the test environment, add `import { webcrypto } from 'crypto'; global.crypto = webcrypto as any;` at the top of the test file only. Check `jest.config.js` or `package.json` jest config first.

---

## Part B — BackendClient

### Task 3: BackendClient

BackendClient wraps `requestUrl` (Obsidian's fetch), injects the `Authorization: Bearer …` header, and transparently refreshes the token when it expires.

The client does **not** persist tokens — it holds a reference to the plugin settings object and updates it in place; the caller is responsible for calling `plugin.saveSettings()` afterwards.

**File:** `obsidian-plugin/src/client.ts`

```typescript
import { requestUrl, RequestUrlParam } from 'obsidian';
import type { PubObsSettings, RepoInfo, TokenResponse } from './types';

export class BackendClient {
  constructor(private settings: PubObsSettings, private saveSettings: () => Promise<void>) {}

  private get baseUrl(): string {
    return this.settings.backendUrl.replace(/\/$/, '');
  }

  private isTokenExpired(): boolean {
    if (!this.settings.accessToken) return true;
    const nowSec = Math.floor(Date.now() / 1000);
    return this.settings.tokenExpiresAt - nowSec < 60;
  }

  async ensureFreshToken(): Promise<void> {
    if (!this.isTokenExpired()) return;
    if (!this.settings.refreshToken) throw new Error('Not authenticated');

    const resp = await requestUrl({
      url: `${this.baseUrl}/auth/refresh`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({ refresh_token: this.settings.refreshToken }),
      throw: false,
    });
    if (resp.status !== 200) throw new Error('Token refresh failed');

    const data: TokenResponse = resp.json;
    this.applyTokens(data);
    await this.saveSettings();
  }

  applyTokens(data: TokenResponse): void {
    this.settings.accessToken = data.access_token;
    this.settings.refreshToken = data.refresh_token;
    this.settings.tokenExpiresAt = Math.floor(Date.now() / 1000) + data.expires_in;
  }

  private async request<T>(params: RequestUrlParam & { url: string }): Promise<T> {
    await this.ensureFreshToken();
    const resp = await requestUrl({
      ...params,
      headers: {
        ...(params.headers ?? {}),
        Authorization: `Bearer ${this.settings.accessToken}`,
      },
      throw: false,
    });
    if (resp.status >= 400) {
      const msg = resp.json?.error ?? `HTTP ${resp.status}`;
      throw new Error(msg);
    }
    return resp.json as T;
  }

  async getMe(): Promise<{ id: string; email: string; is_instance_admin: boolean }> {
    return this.request({ url: `${this.baseUrl}/api/me` });
  }

  async listRepos(): Promise<RepoInfo[]> {
    return this.request({ url: `${this.baseUrl}/api/repos` });
  }

  async upsertFolderMapping(repoId: string, vaultFolder: string, subfolder: string): Promise<void> {
    await this.request({
      url: `${this.baseUrl}/api/me/folder-mappings/${repoId}`,
      method: 'PUT',
      contentType: 'application/json',
      body: JSON.stringify({ vault_folder: vaultFolder, subfolder }),
    });
  }

  async exchangeToken(code: string, codeVerifier: string): Promise<TokenResponse> {
    const resp = await requestUrl({
      url: `${this.baseUrl}/auth/token`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({ code, code_verifier: codeVerifier }),
      throw: false,
    });
    if (resp.status !== 200) throw new Error('Token exchange failed');
    return resp.json as TokenResponse;
  }

  async sync(repoId: string, files: SyncFile[]): Promise<{ commit_sha: string }> {
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/sync`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({ files }),
    });
  }
}

export interface SyncFile {
  path: string;
  md_content: string;
  html_content: string;
  frontmatter: Record<string, unknown>;
}
```

- [ ] **Step 1: Write `src/client.ts`** with the content above.

---

### Task 4: BackendClient unit tests

Tests mock `requestUrl` so they run outside Obsidian.

**File:** `obsidian-plugin/tests/client.test.ts`

```typescript
jest.mock('obsidian', () => ({
  requestUrl: jest.fn(),
}), { virtual: true });

import { requestUrl } from 'obsidian';
import { BackendClient } from '../src/client';
import type { PubObsSettings } from '../src/types';
import { DEFAULT_SETTINGS } from '../src/types';

const mockRequestUrl = requestUrl as jest.MockedFunction<typeof requestUrl>;

function makeSettings(overrides: Partial<PubObsSettings> = {}): PubObsSettings {
  return {
    ...DEFAULT_SETTINGS,
    backendUrl: 'http://localhost:8080',
    accessToken: 'access-token',
    refreshToken: 'refresh-token',
    tokenExpiresAt: Math.floor(Date.now() / 1000) + 3600, // fresh
    ...overrides,
  };
}

describe('BackendClient', () => {
  let settings: PubObsSettings;
  let save: jest.Mock;
  let client: BackendClient;

  beforeEach(() => {
    settings = makeSettings();
    save = jest.fn().mockResolvedValue(undefined);
    client = new BackendClient(settings, save);
    mockRequestUrl.mockReset();
  });

  test('injects Authorization header on authenticated requests', async () => {
    mockRequestUrl.mockResolvedValue({ status: 200, json: [], headers: {}, text: '[]' } as any);
    await client.listRepos();
    expect(mockRequestUrl).toHaveBeenCalledWith(
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer access-token' }),
      })
    );
  });

  test('refreshes token when near expiry', async () => {
    settings.tokenExpiresAt = Math.floor(Date.now() / 1000) + 30; // about to expire
    settings.accessToken = 'old-token';

    mockRequestUrl
      .mockResolvedValueOnce({
        status: 200,
        json: { access_token: 'new-token', refresh_token: 'new-refresh', expires_in: 3600 },
        headers: {},
        text: '',
      } as any)
      .mockResolvedValueOnce({ status: 200, json: [], headers: {}, text: '[]' } as any);

    await client.listRepos();

    expect(settings.accessToken).toBe('new-token');
    expect(save).toHaveBeenCalledTimes(1);
  });

  test('throws when refresh fails', async () => {
    settings.tokenExpiresAt = 0;
    mockRequestUrl.mockResolvedValue({ status: 401, json: { error: 'unauthorized' }, headers: {}, text: '' } as any);
    await expect(client.listRepos()).rejects.toThrow('Token refresh failed');
  });

  test('exchangeToken posts code and verifier', async () => {
    mockRequestUrl.mockResolvedValue({
      status: 200,
      json: { access_token: 'tok', refresh_token: 'ref', expires_in: 3600 },
      headers: {},
      text: '',
    } as any);
    const result = await client.exchangeToken('auth-code', 'verifier');
    expect(result.access_token).toBe('tok');
    expect(mockRequestUrl).toHaveBeenCalledWith(
      expect.objectContaining({
        url: 'http://localhost:8080/auth/token',
        method: 'POST',
        body: JSON.stringify({ code: 'auth-code', code_verifier: 'verifier' }),
      })
    );
  });
});
```

- [ ] **Step 1: Write `tests/client.test.ts`** with the content above.
- [ ] **Step 2: Verify** — `npm test` — all client tests pass alongside pkce tests.

---

## Part C — Auth flow

### Task 5: Auth session state

Holds in-memory state for the pending PKCE session (verifier + state) during the browser redirect round-trip.

**File:** `obsidian-plugin/src/auth.ts`

```typescript
import { generateVerifier, generateState, computeChallenge } from './pkce';
import type { BackendClient } from './client';

interface PendingAuth {
  verifier: string;
  state: string;
}

export class AuthFlow {
  private pending: PendingAuth | null = null;

  constructor(
    private client: BackendClient,
    private backendUrl: () => string,
  ) {}

  async beginAuth(): Promise<void> {
    const verifier = generateVerifier();
    const state = generateState();
    const challenge = await computeChallenge(verifier);
    this.pending = { verifier, state };

    const redirectUri = 'obsidian://pubobs/callback';
    const base = this.backendUrl().replace(/\/$/, '');
    const url =
      `${base}/auth/plugin` +
      `?code_challenge=${encodeURIComponent(challenge)}` +
      `&code_challenge_method=S256` +
      `&redirect_uri=${encodeURIComponent(redirectUri)}` +
      `&state=${encodeURIComponent(state)}`;

    window.open(url);
  }

  async handleCallback(
    params: Record<string, string>,
    onSuccess: () => Promise<void>,
    onError: (msg: string) => void,
  ): Promise<void> {
    if (!this.pending) {
      onError('No pending auth session');
      return;
    }
    if (params['state'] !== this.pending.state) {
      onError('State mismatch — possible CSRF');
      this.pending = null;
      return;
    }
    const code = params['code'];
    if (!code) {
      onError('Missing code in callback');
      this.pending = null;
      return;
    }
    try {
      const tokens = await this.client.exchangeToken(code, this.pending.verifier);
      this.client.applyTokens(tokens);
      await onSuccess();
    } catch (e: unknown) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      this.pending = null;
    }
  }
}
```

- [ ] **Step 1: Write `src/auth.ts`** with the content above.

---

### Task 6: Auth flow tests

**File:** `obsidian-plugin/tests/auth.test.ts`

```typescript
jest.mock('obsidian', () => ({ requestUrl: jest.fn() }), { virtual: true });

import { AuthFlow } from '../src/auth';
import type { BackendClient } from '../src/client';
import type { TokenResponse } from '../src/types';

function makeClient(exchangeResult: TokenResponse | Error): jest.Mocked<BackendClient> {
  return {
    exchangeToken: jest.fn().mockImplementation(() =>
      exchangeResult instanceof Error ? Promise.reject(exchangeResult) : Promise.resolve(exchangeResult)
    ),
    applyTokens: jest.fn(),
  } as unknown as jest.Mocked<BackendClient>;
}

describe('AuthFlow.handleCallback', () => {
  const goodTokens: TokenResponse = { access_token: 'a', refresh_token: 'r', expires_in: 3600 };

  beforeEach(() => {
    (global as any).window = { open: jest.fn() };
    (global as any).crypto = require('crypto').webcrypto;
  });

  test('rejects when no pending session', async () => {
    const client = makeClient(goodTokens);
    const flow = new AuthFlow(client, () => 'http://localhost:8080');
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state: 's' }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('No pending auth session');
  });

  test('rejects on state mismatch', async () => {
    const client = makeClient(goodTokens);
    const flow = new AuthFlow(client, () => 'http://localhost:8080');
    await flow.beginAuth();
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state: 'wrong-state' }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('State mismatch — possible CSRF');
  });

  test('calls applyTokens and onSuccess on valid callback', async () => {
    const client = makeClient(goodTokens);
    const flow = new AuthFlow(client, () => 'http://localhost:8080');
    await flow.beginAuth();

    // Capture the state that was set
    const openCall = (window.open as jest.Mock).mock.calls[0][0] as string;
    const stateMatch = openCall.match(/state=([^&]+)/);
    const state = decodeURIComponent(stateMatch![1]);

    const onSuccess = jest.fn().mockResolvedValue(undefined);
    const onError = jest.fn();
    await flow.handleCallback({ code: 'auth-code', state }, onSuccess, onError);

    expect(client.applyTokens).toHaveBeenCalledWith(goodTokens);
    expect(onSuccess).toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });

  test('clears pending after successful exchange', async () => {
    const client = makeClient(goodTokens);
    const flow = new AuthFlow(client, () => 'http://localhost:8080');
    await flow.beginAuth();

    const openCall = (window.open as jest.Mock).mock.calls[0][0] as string;
    const state = decodeURIComponent(openCall.match(/state=([^&]+)/)![1]);

    await flow.handleCallback({ code: 'c', state }, async () => {}, jest.fn());

    // second callback with same state should fail (pending is cleared)
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('No pending auth session');
  });
});
```

- [ ] **Step 1: Write `tests/auth.test.ts`** with the content above.
- [ ] **Step 2: Verify** — `npm test` — all auth tests pass.

---

## Part D — Settings tab + sync command + main plugin

### Task 7: Sync manager

Reads vault files in the configured folder, builds the payload, calls the backend.

**File:** `obsidian-plugin/src/sync.ts`

```typescript
import { App, TFile, Notice } from 'obsidian';
import type { BackendClient, SyncFile } from './client';
import type { PubObsSettings } from './types';

export class SyncManager {
  constructor(
    private app: App,
    private client: BackendClient,
    private settings: PubObsSettings,
  ) {}

  async syncRepo(repoId: string): Promise<void> {
    const mapping = this.settings.repoMappings[repoId];
    if (!mapping) throw new Error(`No folder mapping for repo ${repoId}`);

    const vaultFolder = mapping.vaultFolder;
    const files = this.app.vault
      .getFiles()
      .filter(f => f.extension === 'md' && (vaultFolder === '' || f.path.startsWith(vaultFolder + '/')));

    const syncFiles: SyncFile[] = await Promise.all(
      files.map(f => this.buildSyncFile(f, vaultFolder, mapping.subfolder))
    );

    const result = await this.client.sync(repoId, syncFiles);
    new Notice(`Synced ${syncFiles.length} file(s) — ${result.commit_sha.slice(0, 7)}`);
  }

  private async buildSyncFile(file: TFile, vaultFolder: string, subfolder: string): Promise<SyncFile> {
    const content = await this.app.vault.read(file);
    const cache = this.app.metadataCache.getFileCache(file);
    const { position: _pos, ...frontmatter } = (cache?.frontmatter ?? {}) as Record<string, unknown>;

    // Strip leading vaultFolder prefix to get relative path, then prepend subfolder
    let relative = file.path;
    if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
      relative = relative.slice(vaultFolder.length + 1);
    }
    const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;

    return { path: repoPath, md_content: content, html_content: '', frontmatter };
  }
}
```

- [ ] **Step 1: Write `src/sync.ts`** with the content above.

---

### Task 8: Settings tab

**File:** `obsidian-plugin/src/settings.ts`

```typescript
import { App, PluginSettingTab, Setting, Notice } from 'obsidian';
import type PubObsPlugin from './main';
import type { RepoInfo } from './types';

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
        new Setting(containerEl)
          .setName(mapping.repoName)
          .setDesc(`Repo ID: ${repoId}`)
          .addText(text =>
            text
              .setPlaceholder('Vault folder (e.g. Notes/Published)')
              .setValue(mapping.vaultFolder)
              .onChange(async v => {
                this.plugin.settings.repoMappings[repoId].vaultFolder = v.trim();
                await this.plugin.saveSettings();
                await this.plugin.client.upsertFolderMapping(repoId, v.trim(), mapping.subfolder);
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
```

- [ ] **Step 1: Write `src/settings.ts`** with the content above.

---

### Task 9: Main plugin class

**File:** `obsidian-plugin/src/main.ts`

```typescript
import { Plugin, Notice } from 'obsidian';
import { BackendClient } from './client';
import { AuthFlow } from './auth';
import { SyncManager } from './sync';
import { PubObsSettingTab } from './settings';
import { DEFAULT_SETTINGS, type PubObsSettings, type RepoInfo } from './types';

export default class PubObsPlugin extends Plugin {
  settings!: PubObsSettings;
  client!: BackendClient;
  authFlow!: AuthFlow;
  syncManager!: SyncManager;

  async onload(): Promise<void> {
    await this.loadSettings();

    this.client = new BackendClient(this.settings, () => this.saveSettings());
    this.authFlow = new AuthFlow(this.client, () => this.settings.backendUrl);
    this.syncManager = new SyncManager(this.app, this.client, this.settings);

    this.registerObsidianProtocolHandler('pubobs/callback', async params => {
      await this.authFlow.handleCallback(
        params,
        async () => {
          await this.saveSettings();
          new Notice('PubObs: signed in successfully');
          await this.refreshRepoList();
        },
        msg => new Notice(`PubObs auth error: ${msg}`),
      );
    });

    this.addCommand({
      id: 'sync-all',
      name: 'Sync all repos',
      callback: async () => {
        const repoIds = Object.keys(this.settings.repoMappings);
        if (repoIds.length === 0) {
          new Notice('PubObs: no repos configured — open Settings to add one');
          return;
        }
        for (const id of repoIds) {
          try {
            await this.syncManager.syncRepo(id);
          } catch (e: unknown) {
            new Notice(`PubObs sync failed (${id}): ` + (e instanceof Error ? e.message : String(e)));
          }
        }
      },
    });

    this.addSettingTab(new PubObsSettingTab(this.app, this));
  }

  async loadSettings(): Promise<void> {
    this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
  }

  async refreshRepoList(): Promise<void> {
    const repos: RepoInfo[] = await this.client.listRepos();
    for (const repo of repos) {
      if (!this.settings.repoMappings[repo.id]) {
        this.settings.repoMappings[repo.id] = {
          repoName: repo.name,
          vaultFolder: '',
          subfolder: '',
        };
      } else {
        this.settings.repoMappings[repo.id].repoName = repo.name;
      }
    }
    await this.saveSettings();
  }
}
```

- [ ] **Step 1: Write `src/main.ts`** with the content above.
- [ ] **Step 2: Build** — `npm run build` — must succeed with no TypeScript errors.
- [ ] **Step 3: Full test run** — `npm test` — all tests pass (pkce + client + auth).

---

## Part D — verification checklist

After all tasks complete:

- [ ] `npm test` — all suites green
- [ ] `npm run build` — outputs `main.js` with no TypeScript errors
- [ ] `manifest.json` version updated to `0.2.0`
- [ ] No references to `isomorphic-git` remain in `src/`
- [ ] `node_modules/isomorphic-git` is absent (verify with `ls node_modules | grep iso`)

---

## Appendix — Backend API reference

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| `GET` | `/auth/plugin` | none | `?code_challenge=…&redirect_uri=obsidian://pubobs/callback&state=…` |
| `POST` | `/auth/token` | none | `{code, code_verifier}` → `{access_token, refresh_token, expires_in}` |
| `POST` | `/auth/refresh` | none | `{refresh_token}` → same as above |
| `GET` | `/api/me` | Bearer | `{id, email, is_instance_admin}` |
| `GET` | `/api/repos` | Bearer | `[{id, name, remote_url, default_branch}]` |
| `GET` | `/api/me/folder-mappings` | Bearer | `[{id, user_id, repo_id, vault_folder, subfolder}]` |
| `PUT` | `/api/me/folder-mappings/{repoID}` | Bearer | `{vault_folder, subfolder}` |
| `POST` | `/api/repos/{id}/sync` | Bearer | `{files: [{path, md_content, html_content, frontmatter}]}` → `{commit_sha}` |
