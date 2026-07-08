import { fullPageLoader } from './spinner';

type ViewFactory = (params: Record<string, string>) => HTMLElement | Promise<HTMLElement>;

interface Route {
  pattern: RegExp;
  keys: string[];
  factory: ViewFactory;
}

const routes: Route[] = [];
let container: HTMLElement;

export function register(path: string, factory: ViewFactory): void {
  const keys: string[] = [];
  let src = path;
  let wildcard = false;

  if (src.endsWith('/*')) {
    src = src.slice(0, -2);
    wildcard = true;
  }

  src = src.replace(/:([^/]+)/g, (_: string, k: string) => {
    keys.push(k);
    return '([^/]+)';
  });

  const pattern = wildcard
    ? new RegExp(`^${src}(?:/(.*))?$`)
    : new RegExp(`^${src}$`);

  if (wildcard) keys.push('*');
  routes.push({ pattern, keys, factory });
}

export function navigate(hash: string): void {
  location.hash = hash;
}

export function start(root: HTMLElement): void {
  container = root;
  window.addEventListener('hashchange', () => void render());
  void render();
}

// Reader-only theming that must never bleed into non-reader routes: the
// reader's own dark-mode body class, and — critically — the vault's raw
// Obsidian theme CSS that reader-note.ts injects to render notes faithfully.
// That stylesheet is an arbitrary, unscoped author theme (not written for
// this app), so it styles bare tags/vars anywhere on the page; leaving it in
// <head> after navigating away made admin pages look randomly, partially
// dark. Views that need this theming (currently only the reader) re-apply
// it themselves on entry, so clearing it here is always safe.
function clearReaderTheming(): void {
  document.body.classList.remove('pubobs-reader-theme');
  document.getElementById('obsidian-theme-css')?.remove();
}

async function render(): Promise<void> {
  const hash = location.hash.replace(/^#\/?/, '') || '/';
  const path = hash.startsWith('/') ? hash : '/' + hash;
  const isReaderRoute = path.startsWith('/read/');
  // Skip the reset while moving between two reader pages (e.g. note → note)
  // so the theme doesn't flash off and back on for no reason.
  if (!isReaderRoute) clearReaderTheming();
  for (const route of routes) {
    const m = path.match(route.pattern);
    if (!m) continue;
    const params: Record<string, string> = {};
    route.keys.forEach((k, i) => { params[k] = m[i + 1]; });
    // Views are async (they fetch data before returning their DOM), so show a
    // spinner right away instead of leaving the previous/blank page on screen.
    container.innerHTML = '';
    container.appendChild(fullPageLoader());
    const el = await route.factory(params);
    container.innerHTML = '';
    container.appendChild(el);
    return;
  }
  clearReaderTheming();
  container.innerHTML = `<p style="padding:2rem;color:#888">Page not found: ${path}</p>`;
}
