import { requestUrl, RequestUrlParam } from 'obsidian';
import type { PubObsSettings, RepoInfo, TokenResponse, FileEntry } from './types';

// Sync payloads (vault CSS + rendered notes/assets) can run tens of MB
// uncompressed; gzip shrinks JSON/base64 text by roughly 60-80%.
async function gzipCompress(text: string): Promise<ArrayBuffer> {
  const stream = new Blob([text]).stream().pipeThrough(new CompressionStream('gzip'));
  return new Response(stream).arrayBuffer();
}

export interface SyncFile {
  path: string;
  md_content: string;
  encrypted_html: string;
  frontmatter: Record<string, unknown>;
}

export interface SyncAsset {
  path: string;       // repo-relative path (e.g. attachments/diagram.png)
  content: string;    // base64-encoded binary
}

/** URL-encode each path segment so Cyrillic/spaces survive routing intact. */
export function encodeNotePath(notePath: string): string {
  return notePath.split('/').map(encodeURIComponent).join('/');
}

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

    this.applyTokens(resp.json as TokenResponse);
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
    let body: unknown;
    try {
      body = resp.json;
    } catch {
      // Non-JSON body — PubObs itself always responds with JSON (including
      // its own intentional error statuses, e.g. a 502 it returns when it
      // fails to read a repo's local git state — that case would show up as
      // a normal, specific error message below, not here). Reaching this
      // branch means something in front of PubObs (or PubObs not running at
      // all) returned a non-JSON error page instead — e.g. a reverse-proxy
      // rejection (payload too large, gateway timeout) or the backend being
      // unreachable. The status code alone doesn't tell us which, so avoid
      // guessing a single specific cause here.
      throw new Error(
        `Server returned a non-JSON response (HTTP ${resp.status}). ` +
        'This usually means the request did not reach a running PubObs backend ' +
        '(e.g. a proxy/gateway rejection or timeout, or the backend being down) ' +
        'rather than an error PubObs itself reported.'
      );
    }

    if (resp.status >= 400) {
      const msg = (body as { error?: string })?.error ?? `HTTP ${resp.status}`;
      const err = new Error(msg);
      (err as any).status = resp.status;
      throw err;
    }
    return body as T;
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

  async sync(
    repoId: string, files: SyncFile[], assets: SyncAsset[], deletedPaths: string[],
  ): Promise<{ commit_sha: string; note_keys?: Record<string, string> }> {
    const body = await gzipCompress(JSON.stringify({ files, assets, deleted_paths: deletedPaths }));
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/sync`,
      method: 'POST',
      contentType: 'application/json',
      headers: { 'Content-Encoding': 'gzip' },
      body,
    });
  }

  async listFiles(repoId: string): Promise<FileEntry[]> {
    return this.request({ url: `${this.baseUrl}/api/repos/${repoId}/files` });
  }

  // getNoteKey mints (or fetches the already-minted) backend-authoritative
  // key for a note WITHOUT touching its shared_publicly state — used to
  // learn a brand-new note's key before it can be encrypted for the main
  // sync request, a chicken-and-egg problem /share can't solve (its
  // "restricted" mode never returns a key; its "public" mode would
  // incorrectly make the note public just to learn one).
  async getNoteKey(repoId: string, notePath: string): Promise<string> {
    const resp = await this.request<{ key: string }>({
      url: `${this.baseUrl}/api/repos/${repoId}/notes/${encodeNotePath(notePath)}/key`,
      method: 'POST',
    });
    return resp.key;
  }

  async shareNote(
    repoId: string, notePath: string, mode: 'restricted' | 'public',
  ): Promise<{ shared: boolean; path: string; key?: string }> {
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/notes/${encodeNotePath(notePath)}/share`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({ mode }),
    });
  }

  async setGitCredential(
    repoId: string, opts: { username: string; token: string; gitName?: string; gitEmail?: string },
  ): Promise<void> {
    await this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/git-credential`,
      method: 'PUT',
      contentType: 'application/json',
      body: JSON.stringify({
        username: opts.username,
        token: opts.token,
        git_name: opts.gitName ?? '',
        git_email: opts.gitEmail ?? '',
      }),
    });
  }

  async deleteGitCredential(repoId: string): Promise<void> {
    await this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/git-credential`,
      method: 'DELETE',
    });
  }

  async verifyGitCredential(repoId: string): Promise<{ status: string }> {
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/git-credential/verify`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({}),
    });
  }
}
