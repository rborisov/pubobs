import { requestUrl, RequestUrlParam } from 'obsidian';
import type { PubObsSettings, RepoInfo, TokenResponse, FileEntry } from './types';

export interface SyncFile {
  path: string;
  md_content: string;
  html_content: string;
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
    if (resp.status >= 400) {
      const msg = (resp.json as { error?: string })?.error ?? `HTTP ${resp.status}`;
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

  async sync(repoId: string, files: SyncFile[], assets: SyncAsset[], deletedPaths: string[]): Promise<{ commit_sha: string }> {
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/sync`,
      method: 'POST',
      contentType: 'application/json',
      body: JSON.stringify({ files, assets, deleted_paths: deletedPaths }),
    });
  }

  async listFiles(repoId: string): Promise<FileEntry[]> {
    return this.request({ url: `${this.baseUrl}/api/repos/${repoId}/files` });
  }
}
