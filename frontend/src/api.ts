export interface Repo {
  id: string;
  name: string;
  remote_url: string;
  default_branch: string;
  is_cloned: boolean;
}

export interface RepoAccess {
  id: string;
  repo_id: string;
  principal_type: string;
  principal_id: string;
  role: string;
}

export interface User {
  id: string;
  email: string;
  name: string;
  is_instance_admin: boolean;
  is_banned: boolean;
}

export interface AllowlistEntry {
  id: string;
  pattern: string;
  created_at: string;
}

export interface Me extends User {}

export interface TokenResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

const TOKEN_KEY = 'pubobs_tokens';

interface StoredTokens {
  accessToken: string;
  refreshToken: string;
  expiresAt: number; // unix seconds
}

export const tokenStore = {
  get(): StoredTokens | null {
    const raw = localStorage.getItem(TOKEN_KEY);
    return raw ? (JSON.parse(raw) as StoredTokens) : null;
  },
  set(t: StoredTokens): void {
    localStorage.setItem(TOKEN_KEY, JSON.stringify(t));
  },
  clear(): void {
    localStorage.removeItem(TOKEN_KEY);
  },
  isExpired(): boolean {
    const t = this.get();
    if (!t?.accessToken) return true;
    return t.expiresAt - Math.floor(Date.now() / 1000) < 60;
  },
};

async function refreshTokens(): Promise<void> {
  const t = tokenStore.get();
  if (!t?.refreshToken) throw new Error('Not authenticated');
  const resp = await fetch('/auth/refresh', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: t.refreshToken }),
  });
  if (!resp.ok) throw new Error('Session expired — please sign in again');
  const data: TokenResponse = await resp.json();
  applyTokenResponse(data);
}

export function applyTokenResponse(data: TokenResponse): void {
  tokenStore.set({
    accessToken: data.access_token,
    refreshToken: data.refresh_token,
    expiresAt: Math.floor(Date.now() / 1000) + data.expires_in,
  });
}

async function authedFetch(input: string, init: RequestInit = {}): Promise<Response> {
  if (tokenStore.isExpired()) await refreshTokens();
  const t = tokenStore.get()!;
  const resp = await fetch(input, {
    ...init,
    headers: {
      ...init.headers,
      Authorization: `Bearer ${t.accessToken}`,
    },
  });
  if (resp.status === 401) {
    tokenStore.clear();
    location.hash = '#/login';
    throw new Error('Session expired');
  }
  return resp;
}

async function json<T>(resp: Response): Promise<T> {
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error((err as { error?: string }).error ?? `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

export async function exchangeToken(code: string, verifier: string): Promise<TokenResponse> {
  const resp = await fetch('/auth/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code, code_verifier: verifier }),
  });
  return json<TokenResponse>(resp);
}

export async function getMe(): Promise<Me> {
  return json<Me>(await authedFetch('/api/me'));
}

export async function listRepos(): Promise<Repo[]> {
  return json<Repo[]>(await authedFetch('/api/repos'));
}

export async function createRepo(body: {
  name: string; remote_url: string; default_branch: string;
  username: string; password: string;
}): Promise<{ id: string; name: string }> {
  return json(await authedFetch('/api/admin/repos', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }));
}

export async function updateRepo(id: string, body: {
  name: string; remote_url: string; default_branch: string;
  username: string; password: string;
}): Promise<void> {
  await json(await authedFetch(`/api/admin/repos/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }));
}

export async function deleteRepo(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${id}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function listRepoAccess(repoId: string): Promise<RepoAccess[]> {
  return json<RepoAccess[]>(await authedFetch(`/api/admin/repos/${repoId}/access`));
}

export async function grantAccess(repoId: string, principalId: string, role: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${repoId}/access`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ principal_type: 'user', principal_id: principalId, role }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function revokeAccess(repoId: string, accessId: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${repoId}/access/${accessId}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function listUsers(): Promise<User[]> {
  return json<User[]>(await authedFetch('/api/admin/users'));
}

export async function importRepo(id: string): Promise<{ imported: number }> {
  return json(await authedFetch(`/api/admin/repos/${id}/import`, { method: 'POST' }));
}

export async function setUserAdmin(id: string, admin: boolean): Promise<void> {
  const resp = await authedFetch(`/api/admin/users/${id}/admin`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ admin }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function setUserBanned(id: string, banned: boolean): Promise<void> {
  const resp = await authedFetch(`/api/admin/users/${id}/ban`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ banned }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function listAllowlist(): Promise<AllowlistEntry[]> {
  return json<AllowlistEntry[]>(await authedFetch('/api/admin/allowlist'));
}

export async function addAllowlistEntry(pattern: string): Promise<AllowlistEntry> {
  return json(await authedFetch('/api/admin/allowlist', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ pattern }),
  }));
}

export async function removeAllowlistEntry(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/allowlist/${id}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export interface PubNote {
  id: string;
  path: string;
  title: string;
  synced_at: string;
}

export interface PubRepo {
  id: string;
  name: string;
}

export interface PubNoteDetail {
  id: string;
  path: string;
  title: string;
  html_content: string;
  tags: string[];
  frontmatter: Record<string, unknown>;
  git_commit_sha: string;
  synced_at: string;
  backlinks: Array<{ path: string; title: string }>;
}

export async function pubListNotes(repoId: string): Promise<{ repo: PubRepo; notes: PubNote[] }> {
  const resp = await fetch(`/pub/${repoId}`);
  return json(resp);
}

export async function pubGetNote(repoId: string, notePath: string): Promise<PubNoteDetail> {
  const resp = await fetch(`/pub/${repoId}/notes/${notePath}`);
  return json(resp);
}

export interface PubComment {
  body: string;
  created_at: string;
  author_email: string;
  author_name: string;
}

export async function pubListComments(repoId: string, notePath: string): Promise<PubComment[]> {
  const resp = await fetch(`/pub/${repoId}/notes/${notePath}/comments`);
  return json(resp);
}

export async function addComment(repoId: string, notePath: string, body: string): Promise<void> {
  const resp = await authedFetch(`/api/repos/${repoId}/notes/${notePath}/comments`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ body }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}
