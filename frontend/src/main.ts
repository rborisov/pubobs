import { handleCallbackIfPresent, isAuthenticated, signOut } from './auth';
import { getMe, tokenStore } from './api';
import { register, start, navigate } from './router';
import { loginView } from './views/login';
import { reposView } from './views/repos';
import { repoDetailView } from './views/repo-detail';
import { readerListView } from './views/reader-list';
import { readerNoteView } from './views/reader-note';

register('/login', () => loginView());
register('/repos', () => reposView());
register('/repos/:id', ({ id }) => repoDetailView(id));
register('/read/:repoId', ({ repoId }) => readerListView(repoId));
register('/read/:repoId/*', params => readerNoteView(params['repoId'], params['*'] ?? ''));
register('/', () => {
  navigate(isAuthenticated() ? '/repos' : '/login');
  return document.createElement('div');
});

async function boot(): Promise<void> {
  const app = document.getElementById('app')!;

  try {
    const wasCallback = await handleCallbackIfPresent();
    if (wasCallback) {
      const me = await getMe();
      if (!me.is_instance_admin) {
        tokenStore.clear();
        renderError(app, 'Access denied: instance admin required.<br>Run this SQL to promote your account, then sign in again:<br><code>UPDATE users SET is_instance_admin = 1 WHERE email = \'your@email.com\';</code>');
        return;
      }
      navigate('/repos');
    }
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

  if (!isReaderRoute && (!location.hash || location.hash === '#' || location.hash === '#/')) {
    navigate('/repos');
  }

  if (isAuthenticated() && !isReaderRoute) {
    renderNav(app);
  }

  const content = document.createElement('div');
  content.id = 'content';
  app.appendChild(content);
  start(content);
}

function renderNav(app: HTMLElement): void {
  const nav = document.createElement('nav');
  nav.style.cssText =
    'background:#0f172a;color:#fff;padding:0 24px;height:48px;' +
    'display:flex;align-items:center;gap:16px;font-family:system-ui,sans-serif';
  nav.innerHTML = `
    <span style="font-weight:600;font-size:1rem">PubObs Admin</span>
    <a href="#/repos" style="color:#94a3b8;text-decoration:none;font-size:0.875rem">Repos</a>
    <span style="flex:1"></span>
    <button id="signout-btn"
      style="background:none;border:none;color:#94a3b8;cursor:pointer;font-size:0.875rem">
      Sign out
    </button>
  `;
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

void boot();
