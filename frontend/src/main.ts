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
import { groupsView } from './views/groups';

let currentUser: Me | null = null;

register('/login', () => loginView());
register('/repos', () => reposView(currentUser!));
register('/repos/:id', ({ id }) => repoDetailView(id));
register('/users', () => usersView(currentUser!));
register('/allowlist', () => allowlistView());
register('/dashboard', () => dashboardView(currentUser!));
register('/groups', () => groupsView(currentUser!));
register('/read/:repoId', ({ repoId }) => readerListView(repoId));
register('/read/:repoId/*', params => readerNoteView(params['repoId'], params['*'] ?? ''));
register('/', () => {
  navigate(isAuthenticated()
    ? (currentUser?.is_instance_admin || currentUser?.is_admin ? '/repos' : '/dashboard')
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
      navigate(currentUser.is_instance_admin || currentUser.is_admin ? '/repos' : '/dashboard');
    }
  }

  const content = document.createElement('div');
  content.id = 'content';
  app.appendChild(content);
  start(content);
}

// Inline SVG logo mark — stacked pages with Y-branch diagram
const LOGO_MARK = `
<svg width="32" height="32" viewBox="0 0 44 44" fill="none" xmlns="http://www.w3.org/2000/svg">
  <path d="M4 36 L1 33 L1 9 L4 6 L36 6 L36 36 Z" fill="#8094AF" opacity="0.35"/>
  <path d="M6 39 L3 36 L3 12 L6 9 L39 9 L39 39 Z" fill="#6B7E9E" opacity="0.55"/>
  <path d="M9 42 L6 39 L6 15 L9 12 L42 12 L42 42 Z" fill="#5B6B8E"/>
  <line x1="17" y1="19" x2="26" y2="29" stroke="#2D3F56" stroke-width="2.2" stroke-linecap="round"/>
  <line x1="35" y1="19" x2="26" y2="29" stroke="#4BB585" stroke-width="2.2" stroke-linecap="round"/>
  <line x1="26" y1="29" x2="26" y2="38" stroke="#2D3F56" stroke-width="2.2" stroke-linecap="round"/>
  <circle cx="17" cy="19" r="3.8" fill="#4BB585"/>
  <circle cx="35" cy="19" r="3.8" fill="#4BB585"/>
  <circle cx="26" cy="29" r="3.8" fill="#2D3F56"/>
  <circle cx="26" cy="38" r="3.8" fill="#2D3F56"/>
</svg>`.trim();

function renderNav(app: HTMLElement, me: Me): void {
  const nav = document.createElement('nav');
  nav.style.cssText =
    'background:#2D3F56;color:#fff;padding:0 20px;height:52px;' +
    'display:flex;align-items:center;gap:16px;font-family:system-ui,sans-serif;' +
    'box-shadow:0 1px 3px rgba(0,0,0,0.2)';

  const linkStyle = 'color:#a8bbd0;text-decoration:none;font-size:0.875rem;transition:color 0.1s';
  const logoHtml = `
    <a href="${me.is_instance_admin || me.is_admin ? '#/repos' : '#/dashboard'}"
       style="display:flex;align-items:center;gap:8px;text-decoration:none;color:#fff">
      ${LOGO_MARK}
      <span style="font-weight:700;font-size:1rem;letter-spacing:-0.01em">
        PubObs${me.is_instance_admin ? ' <span style="font-weight:400;font-size:0.75rem;color:#8094AF;margin-left:2px">admin</span>' : ''}
      </span>
    </a>`;

  if (me.is_instance_admin) {
    nav.innerHTML = `
      ${logoHtml}
      <div style="width:1px;height:20px;background:#3d5470;margin:0 4px"></div>
      <a href="#/repos" style="${linkStyle}">Repos</a>
      <a href="#/users" style="${linkStyle}">Users</a>
      <a href="#/allowlist" style="${linkStyle}">Allowlist</a>
      <span style="flex:1"></span>
      <button id="signout-btn"
        style="background:none;border:none;color:#a8bbd0;cursor:pointer;font-size:0.875rem;padding:6px 10px;
               border-radius:4px;transition:background 0.1s">
        Sign out
      </button>
    `;
  } else if (me.is_admin) {
    nav.innerHTML = `
      ${logoHtml}
      <div style="width:1px;height:20px;background:#3d5470;margin:0 4px"></div>
      <a href="#/repos" style="${linkStyle}">Repos</a>
      <a href="#/groups" style="${linkStyle}">Groups</a>
      <a href="#/users" style="${linkStyle}">Users</a>
      <span style="flex:1"></span>
      <span style="color:#8094AF;font-size:0.8rem">${esc(me.email)}</span>
      <button id="signout-btn"
        style="background:none;border:none;color:#a8bbd0;cursor:pointer;font-size:0.875rem;padding:6px 10px;
               border-radius:4px">
        Sign out
      </button>
    `;
  } else {
    nav.innerHTML = `
      ${logoHtml}
      <div style="width:1px;height:20px;background:#3d5470;margin:0 4px"></div>
      <a href="#/dashboard" style="${linkStyle}">My repos</a>
      <span style="flex:1"></span>
      <span style="color:#8094AF;font-size:0.8rem">${esc(me.email)}</span>
      <button id="signout-btn"
        style="background:none;border:none;color:#a8bbd0;cursor:pointer;font-size:0.875rem;padding:6px 10px;
               border-radius:4px">
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
      <a href="/" style="color:#5B6B8E;font-size:0.875rem">Back to login</a>
    </div>
  `;
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

void boot();
