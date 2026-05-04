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

async function render(): Promise<void> {
  const hash = location.hash.replace(/^#\/?/, '') || '/';
  const path = hash.startsWith('/') ? hash : '/' + hash;
  for (const route of routes) {
    const m = path.match(route.pattern);
    if (!m) continue;
    const params: Record<string, string> = {};
    route.keys.forEach((k, i) => { params[k] = m[i + 1]; });
    const el = await route.factory(params);
    container.innerHTML = '';
    container.appendChild(el);
    return;
  }
  container.innerHTML = `<p style="padding:2rem;color:#888">Page not found: ${path}</p>`;
}
