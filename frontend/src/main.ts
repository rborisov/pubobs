import { handleCallbackIfPresent, isAuthenticated, signOut } from './auth';
import { getMe, tokenStore, type Me } from './api';
import { register, start, navigate } from './router';
import { loginView } from './views/login';
import { reposView } from './views/repos';
import { repoDetailView } from './views/repo-detail';
import { usersView } from './views/users';
import { allowlistView } from './views/allowlist';
import { dashboardView } from './views/dashboard';
import { readerListView } from './views/reader-list';
import { readerNoteView } from './views/reader-note';

let currentUser: Me | null = null;

register('/login', () => loginView());
register('/repos', () => reposView());
register('/repos/:id', ({ id }) => repoDetailView(id));
register('/users', () => usersView());
register('/allowlist', () => allowlistView());
register('/dashboard', () => dashboardView(currentUser!));
register('/read/:repoId', ({ repoId }) => readerListView(repoId));
register('/read/:repoId/*', params => readerNoteView(params['repoId'], params['*'] ?? ''));
register('/', () => {
  navigate(isAuthenticated()
    ? (currentUser?.is_instance_admin ? '/repos' : '/dashboard')
    : '/login');
  return document.createElement('div');
});

async function boot(): Promise<void> {
  const app = document.getElementById('app')!;

  try {
    await handleCallbackIfPresent();
  } catch (e: unknown) {
    renderError(app, e instanceof Error ? e.message : String(e));
    return;
  }

  const isReaderRoute = location.hash.startsWith('#/read');

  if (!isAuthenticated() && !isReaderRoute) {
    navigate('/login');
    const content = document.createElement('div');
    content.id = 'content';
    app.appendChild(content);
    start(content);
    return;
  }

  if (isAuthenticated() && !isReaderRoute) {
    try {
      currentUser = await getMe();
    } catch {
      tokenStore.clear();
      navigate('/login');
      const content = document.createElement('div');
      content.id = 'content';
      app.appendChild(content);
      start(content);
      return;
    }

    renderNav(app, currentUser);

    if (!location.hash || location.hash === '#' || location.hash === '#/') {
      navigate(currentUser.is_instance_admin ? '/repos' : '/dashboard');
    }
  }

  const content = document.createElement('div');
  content.id = 'content';
  app.appendChild(content);
  start(content);
}

function renderNav(app: HTMLElement, me: Me): void {
  const nav = document.createElement('nav');
  nav.style.cssText =
    'background:#0f172a;color:#fff;padding:0 24px;height:48px;' +
    'display:flex;align-items:center;gap:16px;font-family:system-ui,sans-serif';

  const linkStyle = 'color:#94a3b8;text-decoration:none;font-size:0.875rem';

  if (me.is_instance_admin) {
    nav.innerHTML = `
      <span style="font-weight:600;font-size:1rem">PubObs Admin</span>
      <a href="#/repos" style="${linkStyle}">Repos</a>
      <a href="#/users" style="${linkStyle}">Users</a>
      <a href="#/allowlist" style="${linkStyle}">Allowlist</a>
      <span style="flex:1"></span>
      <button id="signout-btn"
        style="background:none;border:none;color:#94a3b8;cursor:pointer;font-size:0.875rem">
        Sign out
      </button>
    `;
  } else {
    nav.innerHTML = `
      <span style="font-weight:600;font-size:1rem">PubObs</span>
      <a href="#/dashboard" style="${linkStyle}">My repos</a>
      <span style="flex:1"></span>
      <span style="color:#64748b;font-size:0.8rem">${esc(me.email)}</span>
      <button id="signout-btn"
        style="background:none;border:none;color:#94a3b8;cursor:pointer;font-size:0.875rem">
        Sign out
      </button>
    `;
  }

  nav.querySelector('#signout-btn')!.addEventListener('click', signOut);
  app.appendChild(nav);
}

function renderError(app: HTMLElement, msg: string): void {
  app.innerHTML = `
    <div style="max-width:480px;margin:120px auto;padding:0 24px;font-family:system-ui,sans-serif">
      <p style="color:#c00">${msg}</p>
      <a href="/" style="color:#0f172a;font-size:0.875rem">Back to login</a>
    </div>
  `;
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

void boot();
