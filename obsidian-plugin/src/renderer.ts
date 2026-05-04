import { App, Component, MarkdownRenderer, TFile } from 'obsidian';

export interface RenderedNote {
  html: string;
  assets: Map<string, ArrayBuffer>; // vault path → binary content
}

export function extractStyles(): string {
  const parts: string[] = [];
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      for (const rule of Array.from(sheet.cssRules)) {
        parts.push(rule.cssText);
      }
    } catch { /* cross-origin sheets */ }
  }
  return parts.join('\n');
}

export async function renderNoteToHTML(
  app: App,
  content: string,
  sourcePath: string,
  repoId: string,
  vaultFolder: string,
  subfolder: string,
): Promise<RenderedNote> {
  const container = document.createElement('div');
  container.style.cssText = 'position:absolute;left:-9999px;top:-9999px;width:800px;visibility:hidden';
  document.body.appendChild(container);

  const component = new Component();
  component.load();

  try {
    // Race the render against a timeout — some plugin post-processors (e.g. Bases) can hang
    // indefinitely if they require a live Obsidian workspace context.
    await Promise.race([
      MarkdownRenderer.render(app, content, container, sourcePath, component),
      new Promise<void>(resolve => setTimeout(resolve, 8000)),
    ]);
    await new Promise(resolve => setTimeout(resolve, 100));
    await waitForStable(container);

    // Strip plugin-injected scripts/stylesheets — Obsidian APIs don't exist in the browser
    for (const el of Array.from(container.querySelectorAll('script, link[rel="stylesheet"]'))) {
      el.remove();
    }

    // Rewrite internal links in the DOM (safe — only changes href, no resource loading)
    rewriteInternalLinks(app, container, sourcePath, repoId, vaultFolder, subfolder);

    // Forward Obsidian's image sizing to the <img> as inline styles.
    // ![[image.jpg|400]] sets width="400" on span.internal-embed; without this the
    // browser ignores the span's width attribute and renders images at full intrinsic size.
    forwardImageSizing(container);

    // Build srcMap: app:// attribute value → public URL.
    // We use getAttribute('src') — the raw attribute that innerHTML serializes —
    // not img.src (the resolved DOM property) to guarantee the key matches the HTML string.
    const { assets, srcMap } = await collectAssets(app, container, sourcePath, repoId, vaultFolder, subfolder);

    // Do src replacement as string operations AFTER detaching from the DOM so Electron
    // never gets a chance to load the rewritten /pub/... URLs as app://obsidian.md/pub/...
    let html = container.innerHTML;
    for (const [attrSrc, publicSrc] of srcMap) {
      html = html.split(`src="${attrSrc}"`).join(`src="${publicSrc}"`);
    }

    return { html, assets };
  } finally {
    component.unload();
    document.body.removeChild(container);
  }
}

async function waitForStable(el: HTMLElement, maxWait = 5000, quietFor = 200): Promise<void> {
  return new Promise(resolve => {
    let timer: ReturnType<typeof setTimeout>;
    const done = () => { observer.disconnect(); resolve(); };
    const observer = new MutationObserver(() => {
      clearTimeout(timer);
      timer = setTimeout(done, quietFor);
    });
    observer.observe(el, { childList: true, subtree: true, attributes: true, characterData: true });
    setTimeout(done, maxWait);
    timer = setTimeout(done, quietFor);
  });
}

async function collectAssets(
  app: App,
  container: HTMLElement,
  sourcePath: string,
  repoId: string,
  vaultFolder: string,
  subfolder: string,
): Promise<{ assets: Map<string, ArrayBuffer>; srcMap: Map<string, string> }> {
  const assets = new Map<string, ArrayBuffer>();  // vault path → binary
  const srcMap = new Map<string, string>();         // attribute src value → public URL

  const addAsset = async (file: TFile, attrSrc: string) => {
    if (!assets.has(file.path)) {
      try { assets.set(file.path, await app.vault.readBinary(file)); } catch { return; }
    }
    // getResourcePath returns the canonical app:// URL Obsidian uses — use it as a
    // second key in case the attribute src and getResourcePath differ slightly.
    const resourcePath = app.vault.getResourcePath(file);
    const publicSrc = `/pub/${repoId}/assets/${assetRepoPath(file.path, vaultFolder, subfolder)}`;
    srcMap.set(attrSrc, publicSrc);
    if (resourcePath !== attrSrc) srcMap.set(resourcePath, publicSrc);
  };

  // Images embedded via ![[...]] — the wrapper span carries data-src with the link text.
  // Use getFirstLinkpathDest (same resolver Obsidian uses) to find the TFile regardless
  // of whether the note uses a short name or a full path.
  for (const embed of Array.from(container.querySelectorAll<HTMLElement>('span.internal-embed[data-src]'))) {
    const dataSrc = embed.getAttribute('data-src') ?? '';
    const img = embed.querySelector('img');
    if (!img || !dataSrc) continue;
    const attrSrc = img.getAttribute('src') ?? '';
    if (!attrSrc) continue;
    const file = app.metadataCache.getFirstLinkpathDest(dataSrc, sourcePath);
    if (!(file instanceof TFile)) continue;
    await addAsset(file, attrSrc);
  }

  // Fallback: standard markdown images ![](path) that Obsidian resolved to an app:// URL.
  for (const img of Array.from(container.querySelectorAll<HTMLImageElement>('img'))) {
    const attrSrc = img.getAttribute('src') ?? '';
    if (!attrSrc.startsWith('app://') || srcMap.has(attrSrc)) continue;
    // Extract filename and let Obsidian's vault index find the file anywhere in the vault.
    const filename = decodeURIComponent(attrSrc).split('/').pop()?.split('?')[0] ?? '';
    const file = app.vault.getFiles().find(f => f.name === filename);
    if (!file) continue;
    await addAsset(file, attrSrc);
  }

  return { assets, srcMap };
}

function forwardImageSizing(container: HTMLElement): void {
  for (const embed of Array.from(container.querySelectorAll<HTMLElement>('span.internal-embed.image-embed'))) {
    const img = embed.querySelector('img');
    if (!img) continue;
    const widthAttr = embed.getAttribute('width');
    if (widthAttr) {
      img.style.width = `${widthAttr}px`;
    }
    img.style.maxWidth = '100%';
    img.style.height = 'auto';
  }
}

function rewriteInternalLinks(
  app: App,
  container: HTMLElement,
  sourcePath: string,
  repoId: string,
  vaultFolder: string,
  subfolder: string,
): void {
  for (const a of Array.from(container.querySelectorAll<HTMLAnchorElement>('a.internal-link'))) {
    const dataHref = a.getAttribute('data-href') ?? '';
    const [linkPath] = dataHref.split('#');
    const target = app.metadataCache.getFirstLinkpathDest(linkPath, sourcePath);
    if (!(target instanceof TFile)) continue;

    let repoPath = target.path;
    if (vaultFolder && repoPath.startsWith(vaultFolder + '/')) {
      repoPath = repoPath.slice(vaultFolder.length + 1);
    }
    if (subfolder) {
      repoPath = `${subfolder.replace(/\/$/, '')}/${repoPath}`;
    }
    a.href = `#/read/${repoId}/${repoPath}`;
    a.removeAttribute('data-href');
    a.classList.remove('internal-link');
  }
}

function assetRepoPath(vaultPath: string, vaultFolder: string, subfolder: string): string {
  let p = vaultPath;
  if (vaultFolder && p.startsWith(vaultFolder + '/')) {
    p = p.slice(vaultFolder.length + 1);
  }
  if (subfolder) {
    p = `${subfolder.replace(/\/$/, '')}/${p}`;
  }
  return p;
}
