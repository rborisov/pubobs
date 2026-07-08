import {
  pubGetNote, pubListComments, addComment, mintShareLink, revokeShareLink,
  type PubNoteDetail, type PubComment, type ShareMode,
} from '../api';
import { isAuthenticated } from '../auth';
import { ensureReaderStyles } from '../reader-styles';
import { loadingRow } from '../spinner';

export async function readerNoteView(repoId: string, rawNotePath: string): Promise<HTMLElement> {
  ensureReaderStyles();

  // The route is registered as '/read/:repoId/*', so rawNotePath is
  // everything after "repoId/" in the hash, e.g. "docs/note.md?key=xyz".
  // Split on the FIRST '?' (standard URL semantics) before decoding anything,
  // so a note path containing '&' or other characters the old "&key"-suffix
  // scheme relied on splitting by can no longer corrupt parsing — only a
  // literal '?' ends the path, and everything after it is parsed as a real
  // query string via URLSearchParams rather than manual index math.
  const qIdx = rawNotePath.indexOf('?');
  const rawPath = qIdx !== -1 ? rawNotePath.slice(0, qIdx) : rawNotePath;
  const rawQuery = qIdx !== -1 ? rawNotePath.slice(qIdx + 1) : '';
  const notePath = decodeURIComponent(rawPath);
  const urlKey = new URLSearchParams(rawQuery).get('key') ?? undefined;

  const wrap = document.createElement('div');
  wrap.style.cssText = 'padding:40px 24px;font-family:system-ui,sans-serif';

  // Inject Obsidian's theme CSS once per repo, with targeted patches for rules
  // that break normal browser layout (overflow:hidden on body, fixed heights,
  // etc.). The router removes this element whenever you leave the reader, so
  // it never bleeds into the admin panel — but within the reader, cache it
  // per repo (rather than once ever) so switching repos picks up that
  // repo's own theme instead of keeping whichever loaded first.
  const existing = document.getElementById('obsidian-theme-css');
  if (existing?.getAttribute('data-repo-id') !== repoId) {
    existing?.remove();
    const cssText = await fetch(`/pub/${repoId}/assets/_pubobs/obsidian.css`)
      .then(r => r.ok ? r.text() : '')
      .catch(() => '');

    if (cssText) {
      const style = document.createElement('style');
      style.id = 'obsidian-theme-css';
      style.setAttribute('data-repo-id', repoId);
      style.textContent = patchObsidianCSS(cssText);
      document.head.appendChild(style);
    }
  }

  let note: PubNoteDetail;
  try {
    // Pass the share key through to the metadata fetch — on a guest-closed
    // repo, pubNoteAccess only grants access when ?key= matches, so omitting
    // it here yields a 404 for share-link-only visitors.
    note = await pubGetNote(repoId, notePath, urlKey);
  } catch (e: unknown) {
    wrap.innerHTML = `
      <a href="#/read/${repoId}" class="r-muted" style="font-size:0.875rem;text-decoration:none">← Back</a>
      <p class="r-error" style="margin-top:16px">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  // The URL's ?key= param is the ONLY source of a decryption key — there is
  // no frontmatter fallback. The backend fully owns key issuance now (see
  // GetOrCreateNoteKey/SetNoteShared); a frontend fallback to a
  // frontmatter-embedded key was the other half of the original leak this
  // phase closes; restoring it would let a stale, no-longer-valid key
  // embedded in old note content work again regardless of share state.
  const effectiveKey = urlKey;

  const back = document.createElement('a');
  back.href = `#/read/${repoId}`;
  back.className = 'r-muted';
  back.style.cssText = 'font-size:0.875rem;text-decoration:none;display:block;margin-bottom:32px';
  back.textContent = '← Back';
  wrap.appendChild(back);

  const article = document.createElement('article');

  const h1 = document.createElement('h1');
  h1.style.cssText = 'margin:0 0 8px;font-size:2rem;font-weight:700;line-height:1.2';
  h1.textContent = note.title;
  article.appendChild(h1);

  const meta = document.createElement('div');
  meta.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:32px;flex-wrap:wrap';

  const date = document.createElement('span');
  date.className = 'r-faint';
  date.style.fontSize = '0.8rem';
  date.textContent = note.synced_at
    ? new Date(note.synced_at).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' })
    : '';
  meta.appendChild(date);

  for (const tag of note.tags ?? []) {
    const badge = document.createElement('span');
    badge.className = 'r-tag';
    badge.textContent = tag;
    meta.appendChild(badge);
  }

  // Sharing controls are editor/admin-only — an empty/guest role (anonymous
  // guest-open visitor OR a share-link-only visitor) never even sees a
  // button, so as not to visually imply sharing is possible for them.
  if (note.role === 'editor' || note.role === 'admin') {
    meta.appendChild(buildShareControl(repoId, notePath, note));
  }

  article.appendChild(meta);

  const content = document.createElement('div');
  content.className = 'markdown-rendered markdown-preview-view';

  // Share-link-only visitors (empty/guest role) must decrypt client-side via
  // /render + ?key=. Repo members who open an old share URL while logged in
  // must NOT take this path — after unshare the URL's key is stale but the
  // server still decrypts for real repo access and returns html_content.
  const shareLinkOnly = !note.role || note.role === '';
  let htmlContent: string;
  if (effectiveKey && shareLinkOnly) {
    try {
      const renderURL = `/pub/${repoId}/render/${notePath.split('/').map(encodeURIComponent).join('/')}?key=${encodeURIComponent(effectiveKey)}`;
      htmlContent = await decryptRenderBlob(renderURL, effectiveKey);
    } catch (e) {
      console.error('[PubObs] decryption failed:', e);
      const msg = e instanceof Error && e.message.includes('404')
        ? 'Note content is not yet available. Ask the note owner to re-sync from Obsidian.'
        : 'Could not decrypt note content.';
      htmlContent = `<p style="color:#888;font-style:italic">${msg}</p>`;
    }
  } else if (note.html_content) {
    htmlContent = note.html_content;
  } else if (note.render_pending || (note.role && note.role !== '')) {
    htmlContent = '<p style="color:#888;font-style:italic">Note content is not yet available. Re-sync this note from Obsidian to refresh it.</p>';
  } else {
    htmlContent = '<p style="color:#888;font-style:italic">Open this note via a shared link to view its content.</p>';
  }
  content.innerHTML = htmlContent;
  for (const cb of Array.from(content.querySelectorAll<HTMLInputElement>('input.task-list-item-checkbox'))) {
    cb.style.setProperty('appearance', 'auto', 'important');
    cb.style.setProperty('-webkit-appearance', 'checkbox', 'important');
    cb.style.setProperty('margin-inline-start', '0', 'important');
    cb.style.setProperty('width', 'auto', 'important');
    cb.style.setProperty('height', 'auto', 'important');
    cb.style.setProperty('opacity', '1', 'important');
    cb.style.setProperty('visibility', 'visible', 'important');
  }
  article.appendChild(content);

  wrap.appendChild(article);

  if (note.backlinks?.length > 0) {
    const section = document.createElement('section');
    section.className = 'r-border-bottom';
    section.style.cssText = 'margin-top:48px;padding-top:24px;border-top:1px solid var(--r-border)';

    const blTitle = document.createElement('h2');
    blTitle.className = 'r-section-heading';
    blTitle.textContent = 'Linked from';
    section.appendChild(blTitle);

    const blList = document.createElement('ul');
    blList.className = 'r-backlink-list';
    for (const bl of note.backlinks) {
      const li = document.createElement('li');
      const a = document.createElement('a');
      a.href = `#/read/${repoId}/${bl.path}`;
      a.textContent = bl.title;
      li.appendChild(a);
      blList.appendChild(li);
    }
    section.appendChild(blList);
    wrap.appendChild(section);
  }

  // Comments — load async, render after page is already shown
  const commentsSection = buildCommentsSection(repoId, notePath, note);
  wrap.appendChild(commentsSection);
  const commentsList = commentsSection.querySelector(`#comments-list-${note.id}`) as HTMLElement;
  loadComments(repoId, notePath, commentsList);

  return wrap;
}

// buildShareControl renders a small popover offering the two canonical share
// links plus (when currently shared) a revoke action. Only ever called for
// editor/admin viewers — see the role check at the call site.
function buildShareControl(repoId: string, notePath: string, note: PubNoteDetail): HTMLElement {
  let shared = note.shared_publicly;

  const wrap = document.createElement('div');
  wrap.className = 'r-share-wrap';
  wrap.style.marginLeft = 'auto';

  const toggleBtn = document.createElement('button');
  toggleBtn.className = 'r-btn-ghost';
  toggleBtn.style.cssText = 'font-size:0.75rem;padding:2px 8px';
  toggleBtn.textContent = shared ? 'Shared ▾' : 'Share ▾';
  wrap.appendChild(toggleBtn);

  let menu: HTMLElement | null = null;

  function closeMenu(): void {
    menu?.remove();
    menu = null;
    document.removeEventListener('click', onDocClick, true);
  }

  function onDocClick(e: MouseEvent): void {
    if (menu && !wrap.contains(e.target as Node)) closeMenu();
  }

  async function copyURL(url: string, item: HTMLElement, label: string): Promise<void> {
    await navigator.clipboard.writeText(url);
    item.textContent = 'Copied!';
    setTimeout(() => { item.textContent = label; }, 1200);
  }

  async function doMint(mode: ShareMode, item: HTMLElement, label: string): Promise<void> {
    item.textContent = 'Copying…';
    try {
      const resp = await mintShareLink(repoId, notePath, mode);
      const base = `${location.origin}/#/read/${repoId}/${notePath}`;
      const url = mode === 'public' && resp.key ? `${base}?key=${resp.key}` : base;
      if (resp.shared) {
        shared = true;
        toggleBtn.textContent = 'Shared ▾';
      }
      await copyURL(url, item, label);
      closeMenu();
    } catch (e: unknown) {
      item.textContent = e instanceof Error ? e.message : String(e);
      setTimeout(() => { item.textContent = label; }, 2000);
    }
  }

  function openMenu(): void {
    if (menu) { closeMenu(); return; }
    menu = document.createElement('div');
    menu.className = 'r-share-menu';

    const restrictedLabel = 'Copy link (people with access)';
    const restrictedItem = document.createElement('button');
    restrictedItem.className = 'r-share-menu-item';
    restrictedItem.textContent = restrictedLabel;
    restrictedItem.addEventListener('click', () => void doMint('restricted', restrictedItem, restrictedLabel));
    menu.appendChild(restrictedItem);

    const publicLabel = 'Copy link (anyone with the link)';
    const publicItem = document.createElement('button');
    publicItem.className = 'r-share-menu-item';
    publicItem.textContent = publicLabel;
    publicItem.addEventListener('click', () => void doMint('public', publicItem, publicLabel));
    menu.appendChild(publicItem);

    if (shared) {
      const revokeLabel = 'Stop sharing';
      const revokeItem = document.createElement('button');
      revokeItem.className = 'r-share-menu-item r-share-revoke';
      revokeItem.textContent = revokeLabel;
      revokeItem.addEventListener('click', async () => {
        revokeItem.textContent = 'Revoking…';
        try {
          await revokeShareLink(repoId, notePath);
          shared = false;
          toggleBtn.textContent = 'Share ▾';
          closeMenu();
        } catch (e: unknown) {
          revokeItem.textContent = e instanceof Error ? e.message : String(e);
          setTimeout(() => { revokeItem.textContent = revokeLabel; }, 2000);
        }
      });
      menu.appendChild(revokeItem);
    }

    wrap.appendChild(menu);
    // Deferred so the click that opened the menu doesn't immediately close it.
    setTimeout(() => document.addEventListener('click', onDocClick, true), 0);
  }

  toggleBtn.addEventListener('click', openMenu);

  return wrap;
}

function buildCommentsSection(repoId: string, notePath: string, note: PubNoteDetail): HTMLElement {
  const section = document.createElement('section');
  section.style.cssText = 'margin-top:48px;padding-top:24px;border-top:1px solid var(--r-border)';

  const h = document.createElement('h2');
  h.className = 'r-section-heading';
  h.style.marginBottom = '16px';
  h.textContent = 'Comments';
  section.appendChild(h);

  const list = document.createElement('div');
  list.id = `comments-list-${note.id}`;
  list.className = 'r-faint';
  list.style.fontSize = '0.875rem';
  list.appendChild(loadingRow('Loading comments…', 14));
  section.appendChild(list);

  // Post form (authenticated) or sign-in prompt
  const formWrap = document.createElement('div');
  formWrap.style.marginTop = '20px';
  if (isAuthenticated()) {
    const ta = document.createElement('textarea');
    ta.placeholder = 'Write a comment…';
    ta.className = 'r-form-input';
    formWrap.appendChild(ta);

    const row = document.createElement('div');
    row.style.cssText = 'display:flex;gap:8px;align-items:center;margin-top:8px';

    const btn = document.createElement('button');
    btn.textContent = 'Post comment';
    btn.className = 'r-btn-primary';
    row.appendChild(btn);

    const err = document.createElement('span');
    err.className = 'r-error';
    err.style.fontSize = '0.8rem';
    row.appendChild(err);
    formWrap.appendChild(row);

    btn.addEventListener('click', async () => {
      const body = ta.value.trim();
      if (!body) return;
      btn.disabled = true;
      err.textContent = '';
      try {
        await addComment(repoId, notePath, body, note.git_commit_sha ?? '');
        ta.value = '';
        await loadComments(repoId, notePath, list);
      } catch (e: unknown) {
        err.textContent = e instanceof Error ? e.message : String(e);
      } finally {
        btn.disabled = false;
      }
    });
  } else {
    const p = document.createElement('p');
    p.className = 'r-muted';
    p.style.fontSize = '0.875rem';
    p.innerHTML = `<a href="#/login" class="r-link">Sign in</a> to leave a comment.`;
    formWrap.appendChild(p);
  }
  section.appendChild(formWrap);

  return section;
}

async function loadComments(repoId: string, notePath: string, list: HTMLElement): Promise<void> {
  let comments: PubComment[];
  try {
    comments = await pubListComments(repoId, notePath);
  } catch {
    list.textContent = 'Could not load comments.';
    list.className = 'r-error';
    return;
  }

  list.textContent = '';
  list.className = '';

  if (comments.length === 0) {
    list.className = 'r-faint';
    list.style.fontSize = '0.875rem';
    list.textContent = 'No comments yet.';
    return;
  }

  list.style.cssText = 'display:flex;flex-direction:column;gap:12px';
  for (const c of comments) {
    const card = document.createElement('div');
    card.className = 'r-comment-card';
    if (c.is_outdated) card.style.opacity = '0.6';

    const meta = document.createElement('div');
    meta.style.cssText = 'display:flex;gap:8px;align-items:baseline;margin-bottom:6px;flex-wrap:wrap';
    meta.innerHTML = `
      <span class="r-comment-author">${esc(c.author_name || c.author_email)}</span>
      <span class="r-comment-date">${new Date(c.created_at).toLocaleDateString(undefined, { year:'numeric', month:'short', day:'numeric' })}</span>
      ${c.is_outdated ? '<span style="font-size:0.75rem;color:var(--r-faint,#999);font-style:italic">added before last edit</span>' : ''}
    `;

    const body = document.createElement('div');
    body.className = 'r-comment-body';
    body.innerHTML = renderCommentBody(c.body);

    card.appendChild(meta);
    card.appendChild(body);
    list.appendChild(card);
  }
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function patchObsidianCSS(css: string): string {
  return css
    // overflow:hidden and overflow:clip both prevent scrolling
    .replace(/\boverflow\s*:\s*hidden\b/g, 'overflow: auto')
    .replace(/\boverflow\s*:\s*clip\b/g, 'overflow: auto')
    .replace(/\boverflow-y\s*:\s*hidden\b/g, 'overflow-y: auto')
    .replace(/\boverflow-y\s*:\s*clip\b/g, 'overflow-y: auto')
    .replace(/\boverflow-x\s*:\s*hidden\b/g, 'overflow-x: auto')
    .replace(/\boverflow-x\s*:\s*clip\b/g, 'overflow-x: auto')
    // CSS size containment locks element dimensions independent of content — kills scrolling
    .replace(/\bcontain\s*:\s*strict\b/g, 'contain: style')
    .replace(/\bcontain\s*:\s*content\b/g, 'contain: style')
    .replace(/\bcontain\s*:[^;}]*\bsize\b[^;}]*/g, 'contain: style')
    // Full-viewport heights lock the page height to the window
    .replace(/\bheight\s*:\s*100vh\b/g, 'height: auto')
    .replace(/\bmin-height\s*:\s*100vh\b/g, 'min-height: 0')
    .replace(/\buser-select\s*:\s*none\b/g, 'user-select: text');
}

function renderCommentBody(text: string): string {
  const safe = esc(text);
  const html = safe
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    .replace(/_(.+?)_/g, '<em>$1</em>')
    .replace(/`([^`]+)`/g, '<code style="background:#f1f5f9;padding:1px 4px;border-radius:3px;font-size:0.85em">$1</code>');
  return html
    .split(/\n\n+/)
    .map(p => `<p style="margin:0 0 6px">${p.replace(/\n/g, '<br>')}</p>`)
    .join('');
}

function base64urlDecode(s: string): Uint8Array {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  const padded = b64 + '='.repeat((4 - b64.length % 4) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

async function decryptRenderBlob(url: string, keyB64: string): Promise<string> {
  const resp = await fetch(url);
  if (!resp.ok) throw new Error(`render fetch failed: ${resp.status}`);
  const encrypted = await resp.arrayBuffer();
  const iv = encrypted.slice(0, 12);
  const ciphertext = encrypted.slice(12);
  const keyBytes = base64urlDecode(keyB64);
  const cryptoKey = await crypto.subtle.importKey('raw', keyBytes.buffer as ArrayBuffer, 'AES-GCM', false, ['decrypt']);
  const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: new Uint8Array(iv) }, cryptoKey, ciphertext);
  return new TextDecoder().decode(plaintext);
}
