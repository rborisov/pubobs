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
      // Non-JSON body — e.g. an nginx error page for an oversized request or a
      // gateway timeout. Surface something actionable instead of the raw
      // "Unexpected token '<'... is not valid JSON" parse error.
      throw new Error(
        `Server returned a non-JSON response (HTTP ${resp.status}). ` +
        'This usually means the request was rejected before reaching PubObs ' +
        '(e.g. payload too large, or a proxy/gateway error).'
      );
    }

    if (resp.status >= 400) {
      const msg = (body as { error?: string })?.error ?? `HTTP ${resp.status}`;
      throw new Error(msg);
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
      url: `${this.baseUrl}/api/repos/${repoId}/notes/${notePath}/key`,
      method: 'POST',
    });
    return resp.key;
  }

  async shareNote(
    repoId: string, notePath: string, mode: 'restricted' | 'public',
  ): Promise<{ shared: boolean; path: string; key?: string }> {
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/notes/${notePath}/share`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({ mode }),
    });
  }
}
