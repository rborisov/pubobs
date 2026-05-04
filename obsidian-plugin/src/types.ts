export interface PubObsSettings {
  backendUrl: string;
  accessToken: string;
  refreshToken: string;
  tokenExpiresAt: number; // Unix seconds; 0 = not set
  repoMappings: Record<string, RepoMapping>; // repoId → mapping
  pullSHAs: Record<string, Record<string, string>>; // repoId → filePath → sha
  syncHashes: Record<string, Record<string, string>>; // repoId → repoPath → content hash
}

export interface RepoMapping {
  repoName: string;    // display name, fetched from /api/repos
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

export interface FileEntry {
  path: string;    // repo-relative path (e.g. "notes/foo.md")
  content: string; // raw markdown content
  sha: string;     // git blob SHA for deduplication
}

export const DEFAULT_SETTINGS: PubObsSettings = {
  backendUrl: '',
  accessToken: '',
  refreshToken: '',
  tokenExpiresAt: 0,
  repoMappings: {},
  pullSHAs: {},
  syncHashes: {},
};
