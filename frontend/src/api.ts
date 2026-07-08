export interface Repo {
  id: string;
  name: string;
  remote_url: string;
  default_branch: string;
  is_cloned: boolean;
  role: string; // "admin" | "editor" | "commentator" | "reader"
  allow_guest: boolean;
  storage_destination_id: string | null;
  migration_status: string; // "idle" | "running" | "done" | "failed"
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
  is_admin: boolean;
}

export interface AllowlistEntry {
  id: string;
  pattern: string;
  created_at: string;
}

export interface Group {
  id: string;
  name: string;
}

export interface GroupMember {
  group_id: string;
  user_id: string;
  role: string; // "member" | "admin"
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

const REQUEST_TIMEOUT_MS = 20000;

/**
 * fetch() has no built-in timeout, so a stalled request (dead connection,
 * unresponsive proxy, backend stuck behind a slow query) used to leave the
 * caller's promise — and the router's full-page spinner — hanging forever
 * with no feedback. This aborts and raises a clear error instead.
 */
async function fetchWithTimeout(input: string, init: RequestInit = {}): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } catch (e: unknown) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      throw new Error('Request timed out — please check your connection and try again.');
    }
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

async function refreshTokens(): Promise<void> {
  const t = tokenStore.get();
  if (!t?.refreshToken) throw new Error('Not authenticated');
  const resp = await fetchWithTimeout('/auth/refresh', {
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
  const resp = await fetchWithTimeout(input, {
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
  const resp = await fetchWithTimeout('/auth/token', {
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
  const resp = await authedFetch(`/api/admin/repos/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error((err as { error?: string }).error ?? `HTTP ${resp.status}`);
  }
}

export async function deleteRepo(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${id}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function setRepoGuestAccess(id: string, allowGuest: boolean): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${id}/guest-access`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ allow_guest: allowGuest }),
  });
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

export async function listGroups(): Promise<Group[]> {
  return json<Group[]>(await authedFetch('/api/admin/groups'));
}

export async function createGroup(name: string): Promise<Group> {
  return json(await authedFetch('/api/admin/groups', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  }));
}

export async function deleteGroup(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${id}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function listGroupMembers(groupId: string): Promise<GroupMember[]> {
  return json<GroupMember[]>(await authedFetch(`/api/admin/groups/${groupId}/members`));
}

export async function addGroupMember(groupId: string, userId: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${groupId}/members`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_id: userId }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function removeGroupMember(groupId: string, userId: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${groupId}/members/${userId}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function setGroupMemberRole(groupId: string, userId: string, role: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${groupId}/members/${userId}/role`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function setUserIsAdmin(id: string, admin: boolean): Promise<void> {
  const resp = await authedFetch(`/api/admin/users/${id}/user-admin`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ admin }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export interface PubNote {
  id: string;
  path: string;
  title: string;
  tags: string[];
  synced_at: string;
  shared_publicly: boolean;
}

export interface PubRepo {
  id: string;
  name: string;
}

export interface PubNoteDetail {
  id: string;
  path: string;
  title: string;
  html_content?: string;
  tags: string[];
  frontmatter: Record<string, unknown>;
  git_commit_sha: string;
  synced_at: string;
  backlinks: Array<{ path: string; title: string }>;
  shared_publicly: boolean;
  // True when the caller has repo access but the encrypted render blob is
  // missing (e.g. deleted during share/unshare heal) — re-sync from Obsidian.
  render_pending?: boolean;
  // "" whenever the caller isn't a real repo member (anonymous guest-open
  // visitor, or a share-link-only visitor) — only "editor"/"admin" should
  // ever see sharing controls.
  role: string;
}

// pubFetch attaches a Bearer token when the user is logged in so private repos
// are accessible to authenticated readers, while still working for guests on
// repos with allow_guest enabled.
async function pubFetch(input: string): Promise<Response> {
  const t = tokenStore.get();
  if (t && !tokenStore.isExpired()) {
    return fetchWithTimeout(input, { headers: { Authorization: `Bearer ${t.accessToken}` } });
  }
  return fetchWithTimeout(input);
}

export interface PubListNotesResponse {
  repo: PubRepo;
  notes: PubNote[];
  // Repo-level role, applies to every row — "" (guest) for anonymous
  // guest-open visitors. Never a share-link-only marker here: this endpoint
  // only ever grants access via repo-level pubRepoAccess.
  role: string;
}

export async function pubListNotes(repoId: string): Promise<PubListNotesResponse> {
  return json(await pubFetch(`/pub/${repoId}`));
}

/** URL-encode each path segment so Cyrillic/spaces survive routing intact. */
export function encodeNotePath(notePath: string): string {
  return notePath.split('/').map(encodeURIComponent).join('/');
}

export async function pubGetNote(repoId: string, notePath: string, key?: string): Promise<PubNoteDetail> {
  const qs = key ? `?key=${encodeURIComponent(key)}` : '';
  return json(await pubFetch(`/pub/${repoId}/notes/${encodeNotePath(notePath)}${qs}`));
}

export type ShareMode = 'restricted' | 'public';

export interface ShareLinkResponse {
  shared: boolean;
  path: string;
  key?: string;
}

// mintShareLink and revokeShareLink hit the same /api/repos/{id}/notes/*
// namespace as addComment above, so they pass notePath through unencoded —
// matching that existing precedent for note-path-containing URLs.
export async function mintShareLink(repoId: string, notePath: string, mode: ShareMode): Promise<ShareLinkResponse> {
  const resp = await authedFetch(`/api/repos/${repoId}/notes/${notePath}/share`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  });
  return json<ShareLinkResponse>(resp);
}

export async function revokeShareLink(repoId: string, notePath: string): Promise<{ shared: boolean }> {
  const resp = await authedFetch(`/api/repos/${repoId}/notes/${notePath}/unshare`, {
    method: 'POST',
  });
  return json<{ shared: boolean }>(resp);
}

export interface PubComment {
  body: string;
  created_at: string;
  author_email: string;
  author_name: string;
  is_outdated: boolean;
}

export async function pubListComments(repoId: string, notePath: string): Promise<PubComment[]> {
  return json(await pubFetch(`/pub/${repoId}/notes/${encodeNotePath(notePath)}/comments`));
}

export async function addComment(repoId: string, notePath: string, body: string, noteCommitSha: string): Promise<void> {
  const resp = await authedFetch(`/api/repos/${repoId}/notes/${notePath}/comments`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ body, note_commit_sha: noteCommitSha }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export interface StorageDestination {
  id: string;
  name: string;
  s3_endpoint: string;
  s3_bucket: string;
  s3_access_key: string;
  s3_region: string;
  s3_use_ssl: boolean;
}

export interface StorageDestinationInput {
  name: string;
  s3_endpoint: string;
  s3_bucket: string;
  s3_access_key: string;
  s3_secret_key: string; // blank on edit = keep existing
  s3_region: string;
  s3_use_ssl: boolean;
}

export interface StorageUsage {
  local: { free_bytes: number; db_bytes: number; repos_bytes: number; renders_bytes: number; assets_bytes: number };
  destinations: { id: string; name: string; renders_bytes: number; assets_bytes: number; error?: string }[];
}

export async function listStorageDestinations(): Promise<StorageDestination[]> {
  return json<StorageDestination[]>(await authedFetch('/api/admin/storage-destinations'));
}

export async function createStorageDestination(input: StorageDestinationInput): Promise<StorageDestination> {
  const resp = await authedFetch('/api/admin/storage-destinations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error((err as { error?: string }).error ?? `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<StorageDestination>;
}

export async function updateStorageDestination(id: string, input: StorageDestinationInput): Promise<void> {
  const resp = await authedFetch(`/api/admin/storage-destinations/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error((err as { error?: string }).error ?? `HTTP ${resp.status}`);
  }
}

export async function deleteStorageDestination(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/storage-destinations/${id}`, { method: 'DELETE' });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error((err as { error?: string }).error ?? `HTTP ${resp.status}`);
  }
}

export async function getStorageUsage(): Promise<StorageUsage> {
  return json<StorageUsage>(await authedFetch('/api/admin/storage-usage'));
}

export interface UpdateStatus {
  status: string; // "idle" | "running" | "done" | "error"
  current: string;
  current_short: string;
  latest: string;
  latest_short: string;
  update_available: boolean;
  maintenance: boolean;
  message?: string;
  log?: string;
  updated_at: string;
}

export async function checkUpdate(): Promise<UpdateStatus> {
  return json<UpdateStatus>(await authedFetch('/api/admin/update/check'));
}

export async function startUpdate(): Promise<UpdateStatus> {
  return json<UpdateStatus>(await authedFetch('/api/admin/update/start', { method: 'POST' }));
}

export async function getUpdateStatus(): Promise<UpdateStatus> {
  return json<UpdateStatus>(await authedFetch('/api/admin/update/status'));
}

export async function assignRepoStorage(repoId: string, destinationId: string | null): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${repoId}/storage`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ storage_destination_id: destinationId }),
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error((err as { error?: string }).error ?? `HTTP ${resp.status}`);
  }
}
