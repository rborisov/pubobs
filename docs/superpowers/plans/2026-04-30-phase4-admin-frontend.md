# Phase 4 — PubObs Admin Frontend

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A minimal vanilla-TypeScript SPA served from the backend at `http://localhost:8080`. Admins sign in via PKCE/OIDC, manage repos (create/edit/delete), and grant/revoke per-user access. Regular users only see the login page (non-admins are rejected with a notice).

**Architecture:** TypeScript + esbuild; no frameworks; output bundled as `backend/frontend/static/app.js`. Auth uses the same PKCE endpoints the plugin uses — the web callback is `{origin}/` with code+state in the query string. Tokens stored in `localStorage`.

**Auth flow summary:**
1. JS generates PKCE verifier+challenge, stores verifier in `sessionStorage`
2. Navigates to `/auth/plugin?code_challenge=…&redirect_uri={origin}/&state=…`
3. Backend serves OIDC → on return serves HTML page that JS-redirects to `{origin}/?code=…&state=…`
4. Page load detects `code` param, exchanges via `POST /auth/token`, stores tokens in `localStorage`, clears URL, pushes `#/repos`

---

## Parts overview (execute one per session)

| Part | Tasks | What it produces |
|------|-------|-----------------|
| **A** | 0–2 | Backend: list-access endpoint; Frontend: scaffold, PKCE, API client |
| **B** | 3–5 | Auth module, main entry point, login view — can sign in via browser |
| **C** | 6–7 | Repos list view + create/edit/delete forms |
| **D** | 8–9 | Repo detail view: access list, grant, revoke; final build verification |

---

## Part A — Backend endpoint + frontend scaffold

### Task 0: Add `GET /api/admin/repos/{id}/access` to backend

The repo detail page needs to list who has access to a repo. `store.ListRepoAccess` already exists; it just needs a handler and route.

**File:** `backend/internal/api/admin.go` — add at bottom:

```go
func handleAdminListRepoAccess(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		repoID := chi.URLParam(r, "id")
		entries, err := deps.Store.ListRepoAccess(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list access failed")
			return
		}
		if entries == nil {
			entries = []*model.RepoAccess{}
		}
		writeJSON(w, http.StatusOK, entries)
	}
}
```

**File:** `backend/internal/api/router.go` — add inside the admin group after the existing access routes:

```go
r.Get("/api/admin/repos/{id}/access", handleAdminListRepoAccess(deps))
```

Also add the missing import if needed: `"github.com/pubobs/backend/internal/model"`.

- [ ] **Step 1:** Add `handleAdminListRepoAccess` to `backend/internal/api/admin.go`.
- [ ] **Step 2:** Register the route in `backend/internal/api/router.go`.
- [ ] **Step 3:** Verify — `cd backend && go build ./...` — no errors.

---

### Task 1: Frontend scaffold

**Directory layout to create:**

```
frontend/
  src/
    pkce.ts
    api.ts
    auth.ts
    router.ts
    views/
      login.ts
      repos.ts
      repo-detail.ts
    main.ts
  package.json
  tsconfig.json
  esbuild.config.mjs
```

- [ ] **Step 1: Create directories**

```bash
mkdir -p frontend/src/views
```

- [ ] **Step 2: Write `frontend/package.json`**

```json
{
  "name": "pubobs-admin",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "node esbuild.config.mjs",
    "build": "tsc --noEmit -p tsconfig.json && node esbuild.config.mjs production"
  },
  "devDependencies": {
    "@types/node": "^20.0.0",
    "esbuild": "^0.25.0",
    "typescript": "^5.3.0"
  }
}
```

- [ ] **Step 3: Write `frontend/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2018",
    "module": "CommonJS",
    "moduleResolution": "node",
    "strict": true,
    "noUnusedLocals": true,
    "lib": ["ES2018", "DOM"],
    "outDir": "dist"
  },
  "include": ["src/**/*.ts"]
}
```

- [ ] **Step 4: Write `frontend/esbuild.config.mjs`**

```js
import esbuild from 'esbuild';
import process from 'process';

const prod = process.argv[2] === 'production';

await esbuild.build({
  entryPoints: ['src/main.ts'],
  bundle: true,
  format: 'iife',
  target: 'es2018',
  outfile: '../backend/frontend/static/app.js',
  sourcemap: prod ? false : 'inline',
  minify: prod,
  logLevel: 'info',
});
```

- [ ] **Step 5: Run `npm install`**

```bash
cd frontend && npm install
```

---

### Task 2: PKCE utilities + API client

**File:** `frontend/src/pkce.ts` — identical to the plugin version:

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

**File:** `frontend/src/api.ts`

```typescript
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
```

- [ ] **Step 1:** Write `frontend/src/pkce.ts`.
- [ ] **Step 2:** Write `frontend/src/api.ts`.
- [ ] **Step 3:** Verify TypeScript — `cd frontend && npx tsc --noEmit` — no errors.

---

## Part B — Auth + router + login view

### Task 3: Auth module

**File:** `frontend/src/auth.ts`

```typescript
import { generateVerifier, generateState, computeChallenge } from './pkce';
import { exchangeToken, applyTokenResponse, tokenStore } from './api';

const VERIFIER_KEY = 'pubobs_pkce_verifier';
const STATE_KEY = 'pubobs_pkce_state';

export async function beginAuth(): Promise<void> {
  const verifier = generateVerifier();
  const state = generateState();
  const challenge = await computeChallenge(verifier);
  sessionStorage.setItem(VERIFIER_KEY, verifier);
  sessionStorage.setItem(STATE_KEY, state);

  const redirectUri = encodeURIComponent(location.origin + '/');
  const url =
    `/auth/plugin` +
    `?code_challenge=${encodeURIComponent(challenge)}` +
    `&code_challenge_method=S256` +
    `&redirect_uri=${redirectUri}` +
    `&state=${encodeURIComponent(state)}`;
  location.href = url;
}

// Returns true if a callback was detected and handled (page should now route to #/repos).
export async function handleCallbackIfPresent(): Promise<boolean> {
  const params = new URLSearchParams(location.search);
  const code = params.get('code');
  const state = params.get('state');
  if (!code || !state) return false;

  // Clean the URL immediately so a refresh doesn't re-run this
  history.replaceState(null, '', '/');

  const storedState = sessionStorage.getItem(STATE_KEY);
  const verifier = sessionStorage.getItem(VERIFIER_KEY);
  sessionStorage.removeItem(STATE_KEY);
  sessionStorage.removeItem(VERIFIER_KEY);

  if (state !== storedState || !verifier) {
    throw new Error('Auth state mismatch — please try signing in again');
  }

  const tokens = await exchangeToken(code, verifier);
  applyTokenResponse(tokens);
  return true;
}

export function isAuthenticated(): boolean {
  return tokenStore.get()?.accessToken != null && tokenStore.get()!.accessToken !== '';
}

export function signOut(): void {
  tokenStore.clear();
  location.hash = '#/login';
}
```

- [ ] **Step 1:** Write `frontend/src/auth.ts`.

---

### Task 4: Router

A minimal hash router. Views are plain functions that return an `HTMLElement`.

**File:** `frontend/src/router.ts`

```typescript
type ViewFactory = (params: Record<string, string>) => HTMLElement | Promise<HTMLElement>;

interface Route {
  pattern: RegExp;
  keys: string[];
  factory: ViewFactory;
}

const routes: Route[] = [];
let container: HTMLElement;

export function register(path: string, factory: ViewFactory): void {
  // Convert "/repos/:id" → regex + key list
  const keys: string[] = [];
  const src = path.replace(/:([^/]+)/g, (_: string, k: string) => { keys.push(k); return '([^/]+)'; });
  routes.push({ pattern: new RegExp(`^${src}$`), keys, factory });
}

export function navigate(hash: string): void {
  location.hash = hash;
}

export function start(root: HTMLElement): void {
  container = root;
  window.addEventListener('hashchange', render);
  render();
}

async function render(): Promise<void> {
  const hash = location.hash.replace(/^#/, '') || '/';
  for (const route of routes) {
    const m = hash.match(route.pattern);
    if (!m) continue;
    const params: Record<string, string> = {};
    route.keys.forEach((k, i) => { params[k] = m[i + 1]; });
    const el = await route.factory(params);
    container.innerHTML = '';
    container.appendChild(el);
    return;
  }
  container.innerHTML = `<p style="padding:2rem;color:#888">Page not found: ${hash}</p>`;
}
```

- [ ] **Step 1:** Write `frontend/src/router.ts`.

---

### Task 5: Login view + `index.html` + `main.ts`

**File:** `frontend/src/views/login.ts`

```typescript
import { beginAuth } from '../auth';

export function loginView(): HTMLElement {
  const div = document.createElement('div');
  div.style.cssText = 'max-width:400px;margin:120px auto;padding:0 24px;font-family:system-ui,sans-serif;text-align:center';
  div.innerHTML = `
    <h1 style="font-size:1.5rem;font-weight:600;margin-bottom:8px">PubObs</h1>
    <p style="color:#555;margin-bottom:24px">Sign in to manage repos and access.</p>
    <button id="signin-btn" style="padding:10px 24px;background:#0f172a;color:#fff;border:none;border-radius:6px;font-size:1rem;cursor:pointer">
      Sign in
    </button>
    <p id="err-msg" style="color:#c00;margin-top:16px;display:none"></p>
  `;
  div.querySelector('#signin-btn')!.addEventListener('click', async () => {
    try {
      await beginAuth();
    } catch (e: unknown) {
      const p = div.querySelector('#err-msg') as HTMLElement;
      p.textContent = e instanceof Error ? e.message : String(e);
      p.style.display = 'block';
    }
  });
  return div;
}
```

**File:** `frontend/src/main.ts`

```typescript
import { handleCallbackIfPresent, isAuthenticated, signOut } from './auth';
import { getMe } from './api';
import { register, start, navigate } from './router';
import { loginView } from './views/login';
import { reposView } from './views/repos';
import { repoDetailView } from './views/repo-detail';

register('/login', () => loginView());
register('/repos', () => reposView());
register('/repos/:id', ({ id }) => repoDetailView(id));
register('/', () => {
  navigate(isAuthenticated() ? '/repos' : '/login');
  return document.createElement('div');
});

async function boot(): Promise<void> {
  const app = document.getElementById('app')!;

  // Handle OIDC callback redirect
  try {
    const wasCallback = await handleCallbackIfPresent();
    if (wasCallback) {
      // Verify admin status
      const me = await getMe();
      if (!me.is_instance_admin) {
        signOut();
        renderError(app, 'Access denied: instance admin required.');
        return;
      }
      navigate('/repos');
    }
  } catch (e: unknown) {
    renderError(app, e instanceof Error ? e.message : String(e));
    return;
  }

  if (!location.hash || location.hash === '#/') {
    navigate(isAuthenticated() ? '/repos' : '/login');
  }

  renderNav(app);
  const content = document.createElement('div');
  content.id = 'content';
  app.appendChild(content);
  start(content);
}

function renderNav(app: HTMLElement): void {
  const nav = document.createElement('nav');
  nav.style.cssText =
    'background:#0f172a;color:#fff;padding:0 24px;height:48px;display:flex;align-items:center;gap:16px;font-family:system-ui,sans-serif';
  nav.innerHTML = `
    <span style="font-weight:600;font-size:1rem">PubObs Admin</span>
    <a href="#/repos" style="color:#94a3b8;text-decoration:none;font-size:0.875rem">Repos</a>
    <span style="flex:1"></span>
    <button id="signout-btn" style="background:none;border:none;color:#94a3b8;cursor:pointer;font-size:0.875rem">Sign out</button>
  `;
  nav.querySelector('#signout-btn')!.addEventListener('click', signOut);
  app.appendChild(nav);
}

function renderError(app: HTMLElement, msg: string): void {
  app.innerHTML = `<p style="padding:2rem;color:#c00;font-family:system-ui,sans-serif">${msg}</p>`;
}

boot();
```

**File:** `backend/frontend/static/index.html` — replace the existing placeholder:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>PubObs Admin</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; }
    body { margin: 0; background: #f8fafc; color: #1a1a1a; }
    a { color: #0f172a; }
    input, select, button { font-family: system-ui, sans-serif; font-size: 0.875rem; }
    table { border-collapse: collapse; width: 100%; }
    th, td { text-align: left; padding: 8px 12px; border-bottom: 1px solid #e2e8f0; }
    th { font-weight: 600; color: #475569; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; }
  </style>
</head>
<body>
  <div id="app"></div>
  <script src="app.js"></script>
</body>
</html>
```

- [ ] **Step 1:** Write `frontend/src/views/login.ts`.
- [ ] **Step 2:** Write `frontend/src/main.ts`.
- [ ] **Step 3:** Write stub `frontend/src/views/repos.ts` — just enough to compile:

```typescript
export function reposView(): HTMLElement {
  const d = document.createElement('div');
  d.textContent = 'Repos (coming soon)';
  return d;
}
```

- [ ] **Step 4:** Write stub `frontend/src/views/repo-detail.ts`:

```typescript
export function repoDetailView(_id: string): HTMLElement {
  const d = document.createElement('div');
  d.textContent = 'Repo detail (coming soon)';
  return d;
}
```

- [ ] **Step 5:** Overwrite `backend/frontend/static/index.html` with the content above.
- [ ] **Step 6:** Build — `cd frontend && npm run build` — outputs `backend/frontend/static/app.js` with no errors.
- [ ] **Step 7:** Smoke-test — `cd backend && go run ./cmd/server` — visit `http://localhost:8080`, click Sign in, complete Google auth, confirm redirect back to `http://localhost:8080/#/repos`.

---

## Part C — Repos list + create/edit/delete

### Task 6: Repos view

The repos view shows a table of repos, a "+ New" button that expands a create form inline, and per-row Edit/Delete actions. Edit opens an inline form replacing the row.

**File:** `frontend/src/views/repos.ts`

```typescript
import { listRepos, createRepo, updateRepo, deleteRepo, type Repo } from '../api';
import { navigate } from '../router';

export async function reposView(): Promise<HTMLElement> {
  const wrap = el('div', { style: 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif' });
  wrap.appendChild(await buildReposPage());
  return wrap;
}

async function buildReposPage(): Promise<DocumentFragment> {
  const frag = document.createDocumentFragment();

  const header = el('div', { style: 'display:flex;align-items:center;margin-bottom:24px' });
  header.innerHTML = `<h2 style="margin:0;font-size:1.25rem;flex:1">Repos</h2>`;
  const newBtn = btn('+ New repo', 'primary');
  header.appendChild(newBtn);
  frag.appendChild(header);

  let repos: Repo[];
  try {
    repos = await listRepos();
  } catch (e: unknown) {
    frag.appendChild(errEl(e));
    return frag;
  }

  const tableWrap = el('div');
  frag.appendChild(tableWrap);
  renderTable(tableWrap, repos, header);

  const formWrap = el('div');
  frag.appendChild(formWrap);

  newBtn.addEventListener('click', () => {
    if (formWrap.firstChild) { formWrap.innerHTML = ''; return; }
    formWrap.appendChild(repoForm(null, async (data) => {
      await createRepo(data);
      repos = await listRepos();
      renderTable(tableWrap, repos, header);
      formWrap.innerHTML = '';
    }, () => { formWrap.innerHTML = ''; }));
  });

  return frag;
}

function renderTable(container: HTMLElement, repos: Repo[], header: HTMLElement): void {
  container.innerHTML = '';
  if (repos.length === 0) {
    container.innerHTML = `<p style="color:#888">No repos yet. Create one above.</p>`;
    return;
  }
  const table = el('table');
  table.innerHTML = `<thead><tr>
    <th>Name</th><th>Remote</th><th>Branch</th><th>Status</th><th></th>
  </tr></thead>`;
  const tbody = el('tbody');
  for (const repo of repos) {
    const row = el('tr');
    row.innerHTML = `
      <td><a href="#/repos/${repo.id}" style="font-weight:500">${esc(repo.name)}</a></td>
      <td style="color:#555;font-size:0.8rem;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(repo.remote_url)}</td>
      <td>${esc(repo.default_branch)}</td>
      <td>${repo.is_cloned ? '● cloned' : '○ not cloned'}</td>
      <td style="white-space:nowrap">
        <button class="edit-btn" style="margin-right:8px;background:none;border:none;cursor:pointer;color:#0f172a;text-decoration:underline">Edit</button>
        <button class="del-btn" style="background:none;border:none;cursor:pointer;color:#c00;text-decoration:underline">Delete</button>
      </td>
    `;
    (row.querySelector('.edit-btn') as HTMLElement).addEventListener('click', () => {
      const editWrap = el('div');
      editWrap.appendChild(repoForm(repo, async (data) => {
        await updateRepo(repo.id, data);
        const fresh = await listRepos();
        renderTable(container, fresh, header);
      }, () => editWrap.remove()));
      row.after(el('tr', {}, [el('td', { colSpan: '5', style: 'padding:0' }, [editWrap])]));
    });
    (row.querySelector('.del-btn') as HTMLElement).addEventListener('click', async () => {
      if (!confirm(`Delete repo "${repo.name}"?`)) return;
      try {
        await deleteRepo(repo.id);
        const fresh = await listRepos();
        renderTable(container, fresh, header);
      } catch (e: unknown) { alert(e instanceof Error ? e.message : String(e)); }
    });
    tbody.appendChild(row);
  }
  table.appendChild(tbody);
  container.appendChild(table);
}

type RepoFormData = { name: string; remote_url: string; default_branch: string; username: string; password: string };

function repoForm(
  existing: Repo | null,
  onSave: (data: RepoFormData) => Promise<void>,
  onCancel: () => void,
): HTMLElement {
  const wrap = el('div', { style: 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:20px;margin:16px 0' });
  wrap.innerHTML = `
    <h3 style="margin:0 0 16px;font-size:1rem">${existing ? 'Edit repo' : 'New repo'}</h3>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
      <label>Name<br><input name="name" value="${esc(existing?.name ?? '')}" style="${inputStyle}" placeholder="my-blog"></label>
      <label>Remote URL<br><input name="remote_url" value="${esc(existing?.remote_url ?? '')}" style="${inputStyle}" placeholder="https://github.com/user/repo.git"></label>
      <label>Default branch<br><input name="default_branch" value="${esc(existing?.default_branch ?? 'main')}" style="${inputStyle}"></label>
      <div></div>
      <label>Git username<br><input name="username" style="${inputStyle}" placeholder="git username or leave blank"></label>
      <label>Password / token<br><input name="password" type="password" style="${inputStyle}" placeholder="token or password"></label>
    </div>
    <div style="margin-top:16px;display:flex;gap:8px">
      <button class="save-btn" style="padding:8px 20px;background:#0f172a;color:#fff;border:none;border-radius:6px;cursor:pointer">Save</button>
      <button class="cancel-btn" style="padding:8px 20px;background:#e2e8f0;border:none;border-radius:6px;cursor:pointer">Cancel</button>
      <span class="form-err" style="color:#c00;align-self:center;display:none"></span>
    </div>
  `;
  wrap.querySelector('.cancel-btn')!.addEventListener('click', onCancel);
  wrap.querySelector('.save-btn')!.addEventListener('click', async () => {
    const v = (n: string) => (wrap.querySelector(`[name="${n}"]`) as HTMLInputElement).value.trim();
    const errEl2 = wrap.querySelector('.form-err') as HTMLElement;
    try {
      errEl2.style.display = 'none';
      await onSave({ name: v('name'), remote_url: v('remote_url'), default_branch: v('default_branch'), username: v('username'), password: v('password') });
    } catch (e: unknown) {
      errEl2.textContent = e instanceof Error ? e.message : String(e);
      errEl2.style.display = 'inline';
    }
  });
  return wrap;
}

const inputStyle = 'width:100%;padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;margin-top:4px';

function el(tag: string, attrs: Record<string, string> = {}, children: HTMLElement[] = []): HTMLElement {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'style') e.setAttribute('style', v);
    else if (k === 'colSpan') (e as HTMLTableCellElement).colSpan = Number(v);
    else e.setAttribute(k, v);
  }
  children.forEach(c => e.appendChild(c));
  return e;
}

function btn(text: string, variant: 'primary' | 'danger' | 'ghost' = 'ghost'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  const styles: Record<string, string> = {
    primary: 'padding:8px 16px;background:#0f172a;color:#fff;border:none;border-radius:6px;cursor:pointer',
    danger: 'padding:8px 16px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer',
    ghost: 'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer',
  };
  b.style.cssText = styles[variant];
  return b;
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function errEl(e: unknown): HTMLElement {
  const p = document.createElement('p');
  p.style.color = '#c00';
  p.textContent = e instanceof Error ? e.message : String(e);
  return p;
}

void navigate;
```

- [ ] **Step 1:** Write `frontend/src/views/repos.ts` — replace the stub.
- [ ] **Step 2:** Build — `npm run build` — no errors.

---

### Task 7: Wire repo navigation

The `navigate` import in repos.ts is unused (row names link directly via `href`). Confirm the table row links (`#/repos/${id}`) work — clicking a repo name routes to the detail view (currently stub). That's sufficient for this task.

- [ ] **Step 1:** Build and verify `npm run build` still clean.

---

## Part D — Repo detail: access management

### Task 8: Repo detail view

**File:** `frontend/src/views/repo-detail.ts` — replace stub:

```typescript
import {
  listRepos, listRepoAccess, grantAccess, revokeAccess, listUsers,
  updateRepo, deleteRepo,
  type Repo, type RepoAccess, type User,
} from '../api';
import { navigate } from '../router';

export async function repoDetailView(id: string): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let repo: Repo | undefined;
  let accessList: RepoAccess[];
  let users: User[];

  try {
    const [repos, access, allUsers] = await Promise.all([
      listRepos(), listRepoAccess(id), listUsers(),
    ]);
    repo = repos.find(r => r.id === id);
    accessList = access;
    users = allUsers;
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  if (!repo) {
    wrap.innerHTML = `<p style="color:#c00">Repo not found.</p>`;
    return wrap;
  }

  render(wrap, repo, accessList, users);
  return wrap;
}

function render(wrap: HTMLElement, repo: Repo, accessList: RepoAccess[], users: User[]): void {
  wrap.innerHTML = '';

  // Header
  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:24px';
  header.innerHTML = `
    <a href="#/repos" style="color:#64748b;text-decoration:none;font-size:0.875rem">← Repos</a>
    <h2 style="margin:0;font-size:1.25rem;flex:1">${esc(repo.name)}</h2>
  `;
  const editBtn = mkBtn('Edit', 'ghost');
  const delBtn = mkBtn('Delete', 'danger');
  header.appendChild(editBtn);
  header.appendChild(delBtn);
  wrap.appendChild(header);

  // Repo info card
  const card = document.createElement('div');
  card.style.cssText = 'background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:24px;font-size:0.875rem';
  card.innerHTML = `
    <div style="display:grid;grid-template-columns:auto 1fr;gap:6px 16px;color:#475569">
      <span style="font-weight:600">Remote</span><span>${esc(repo.remote_url)}</span>
      <span style="font-weight:600">Branch</span><span>${esc(repo.default_branch)}</span>
      <span style="font-weight:600">Status</span><span>${repo.is_cloned ? '● cloned' : '○ not cloned'}</span>
    </div>
  `;
  wrap.appendChild(card);

  // Edit form placeholder
  const editWrap = document.createElement('div');
  wrap.appendChild(editWrap);

  editBtn.addEventListener('click', () => {
    if (editWrap.firstChild) { editWrap.innerHTML = ''; return; }
    editWrap.appendChild(repoEditForm(repo, async (data) => {
      await updateRepo(repo.id, data);
      const repos = await listRepos();
      const fresh = repos.find(r => r.id === repo.id);
      if (fresh) { Object.assign(repo, fresh); }
      editWrap.innerHTML = '';
      render(wrap, repo, accessList, users);
    }, () => { editWrap.innerHTML = ''; }));
  });

  delBtn.addEventListener('click', async () => {
    if (!confirm(`Delete repo "${repo.name}"? This cannot be undone.`)) return;
    try {
      await deleteRepo(repo.id);
      navigate('/repos');
    } catch (e: unknown) { alert(e instanceof Error ? e.message : String(e)); }
  });

  // Access table
  const accessSection = document.createElement('div');
  wrap.appendChild(accessSection);

  const renderAccess = (list: RepoAccess[]) => {
    accessSection.innerHTML = '';
    const h = document.createElement('h3');
    h.style.cssText = 'font-size:1rem;margin:0 0 12px';
    h.textContent = 'Access';
    accessSection.appendChild(h);

    if (list.length === 0) {
      const p = document.createElement('p');
      p.style.color = '#888';
      p.textContent = 'No access entries.';
      accessSection.appendChild(p);
    } else {
      const table = document.createElement('table');
      table.innerHTML = `<thead><tr><th>Type</th><th>User</th><th>Role</th><th></th></tr></thead>`;
      const tbody = document.createElement('tbody');
      for (const entry of list) {
        const user = users.find(u => u.id === entry.principal_id);
        const row = document.createElement('tr');
        row.innerHTML = `
          <td>${esc(entry.principal_type)}</td>
          <td>${esc(user?.email ?? entry.principal_id)}</td>
          <td>${esc(entry.role)}</td>
          <td></td>
        `;
        const revokeBtn = mkBtn('Revoke', 'danger-sm');
        revokeBtn.addEventListener('click', async () => {
          if (!confirm(`Revoke ${entry.role} access for ${user?.email ?? entry.principal_id}?`)) return;
          try {
            await revokeAccess(repo.id, entry.id);
            accessList = accessList.filter(a => a.id !== entry.id);
            renderAccess(accessList);
          } catch (e: unknown) { alert(e instanceof Error ? e.message : String(e)); }
        });
        row.querySelector('td:last-child')!.appendChild(revokeBtn);
        tbody.appendChild(row);
      }
      table.appendChild(tbody);
      accessSection.appendChild(table);
    }

    // Grant form
    accessSection.appendChild(grantForm(users, async (userId, role) => {
      await grantAccess(repo.id, userId, role);
      const fresh = await listRepoAccess(repo.id);
      accessList = fresh;
      renderAccess(accessList);
    }));
  };

  renderAccess(accessList);
}

function grantForm(users: User[], onGrant: (userId: string, role: string) => Promise<void>): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'margin-top:20px;background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:16px';
  wrap.innerHTML = `
    <h4 style="margin:0 0 12px;font-size:0.875rem;font-weight:600">Grant access</h4>
    <div style="display:flex;gap:8px;align-items:flex-end;flex-wrap:wrap">
      <label style="font-size:0.8rem">User
        <select name="user" style="display:block;margin-top:4px;padding:6px;border:1px solid #cbd5e1;border-radius:4px">
          ${users.map(u => `<option value="${esc(u.id)}">${esc(u.email)}</option>`).join('')}
        </select>
      </label>
      <label style="font-size:0.8rem">Role
        <select name="role" style="display:block;margin-top:4px;padding:6px;border:1px solid #cbd5e1;border-radius:4px">
          <option>reader</option>
          <option selected>editor</option>
          <option>admin</option>
        </select>
      </label>
      <button class="grant-btn" style="padding:8px 16px;background:#0f172a;color:#fff;border:none;border-radius:6px;cursor:pointer">Grant</button>
      <span class="grant-err" style="color:#c00;font-size:0.8rem;display:none"></span>
    </div>
  `;
  wrap.querySelector('.grant-btn')!.addEventListener('click', async () => {
    const userId = (wrap.querySelector('[name="user"]') as HTMLSelectElement).value;
    const role = (wrap.querySelector('[name="role"]') as HTMLSelectElement).value;
    const errEl = wrap.querySelector('.grant-err') as HTMLElement;
    try {
      errEl.style.display = 'none';
      await onGrant(userId, role);
    } catch (e: unknown) {
      errEl.textContent = e instanceof Error ? e.message : String(e);
      errEl.style.display = 'inline';
    }
  });
  return wrap;
}

type EditData = { name: string; remote_url: string; default_branch: string; username: string; password: string };

function repoEditForm(repo: Repo, onSave: (d: EditData) => Promise<void>, onCancel: () => void): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:20px;margin-bottom:16px';
  const s = 'width:100%;padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;margin-top:4px';
  wrap.innerHTML = `
    <h3 style="margin:0 0 16px;font-size:1rem">Edit repo</h3>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
      <label>Name<br><input name="name" value="${esc(repo.name)}" style="${s}"></label>
      <label>Remote URL<br><input name="remote_url" value="${esc(repo.remote_url)}" style="${s}"></label>
      <label>Default branch<br><input name="default_branch" value="${esc(repo.default_branch)}" style="${s}"></label>
      <div></div>
      <label>Git username<br><input name="username" style="${s}" placeholder="leave blank to keep existing"></label>
      <label>Password / token<br><input name="password" type="password" style="${s}" placeholder="leave blank to keep existing"></label>
    </div>
    <div style="margin-top:16px;display:flex;gap:8px">
      <button class="save" style="padding:8px 20px;background:#0f172a;color:#fff;border:none;border-radius:6px;cursor:pointer">Save</button>
      <button class="cancel" style="padding:8px 20px;background:#e2e8f0;border:none;border-radius:6px;cursor:pointer">Cancel</button>
      <span class="err" style="color:#c00;align-self:center;display:none"></span>
    </div>
  `;
  wrap.querySelector('.cancel')!.addEventListener('click', onCancel);
  wrap.querySelector('.save')!.addEventListener('click', async () => {
    const v = (n: string) => (wrap.querySelector(`[name="${n}"]`) as HTMLInputElement).value.trim();
    const errEl = wrap.querySelector('.err') as HTMLElement;
    try {
      errEl.style.display = 'none';
      await onSave({ name: v('name'), remote_url: v('remote_url'), default_branch: v('default_branch'), username: v('username'), password: v('password') });
    } catch (e: unknown) {
      errEl.textContent = e instanceof Error ? e.message : String(e);
      errEl.style.display = 'inline';
    }
  });
  return wrap;
}

function mkBtn(text: string, variant: 'ghost' | 'danger' | 'danger-sm'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  const styles: Record<string, string> = {
    ghost: 'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer',
    danger: 'padding:8px 16px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer',
    'danger-sm': 'padding:4px 10px;background:none;border:none;color:#dc2626;cursor:pointer;text-decoration:underline;font-size:0.8rem',
  };
  b.style.cssText = styles[variant];
  return b;
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
```

- [ ] **Step 1:** Write `frontend/src/views/repo-detail.ts` — replace stub.
- [ ] **Step 2:** Build — `npm run build` — no errors.

---

### Task 9: Final verification

- [ ] **Step 1:** Full backend build — `cd backend && go build ./...` — no errors.
- [ ] **Step 2:** Frontend build — `cd frontend && npm run build` — no errors.
- [ ] **Step 3:** Start backend — `cd backend && go run ./cmd/server` (with required env vars).
- [ ] **Step 4:** Visit `http://localhost:8080` — shows login page.
- [ ] **Step 5:** Click Sign in — completes OIDC — lands on repos list.
- [ ] **Step 6:** Create a repo — form submits, row appears in table.
- [ ] **Step 7:** Click repo name — detail page loads, grant access to a user, confirm row appears.
- [ ] **Step 8:** Revoke access — row disappears.
- [ ] **Step 9:** Sign out — redirected to login page.

---

## Appendix — Backend API used by the admin panel

| Method | Path | Notes |
|--------|------|-------|
| `GET` | `/auth/plugin` | Begin PKCE; redirect_uri = `{origin}/` |
| `POST` | `/auth/token` | Exchange code |
| `POST` | `/auth/refresh` | Refresh tokens |
| `GET` | `/api/me` | Verify admin flag |
| `GET` | `/api/repos` | All repos (admin sees all) |
| `POST` | `/api/admin/repos` | Create repo |
| `PUT` | `/api/admin/repos/{id}` | Update repo |
| `DELETE` | `/api/admin/repos/{id}` | Delete repo |
| `GET` | `/api/admin/repos/{id}/access` | List access entries *(new)* |
| `POST` | `/api/admin/repos/{id}/access` | Grant access |
| `DELETE` | `/api/admin/repos/{id}/access/{accessID}` | Revoke access |
| `GET` | `/api/admin/users` | All users |
