import { pubGetNote, pubListComments, addComment, type PubNoteDetail, type PubComment } from '../api';
import { isAuthenticated } from '../auth';

export async function readerNoteView(repoId: string, notePath: string): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:720px;margin:0 auto;padding:40px 24px;font-family:system-ui,sans-serif';

  // Inject Obsidian's theme CSS once per page load, with targeted patches for rules
  // that break normal browser layout (overflow:hidden on body, fixed heights, etc.)
  if (!document.getElementById('obsidian-theme-css')) {
    const cssText = await fetch(`/pub/${repoId}/assets/_pubobs/obsidian.css`)
      .then(r => r.ok ? r.text() : '')
      .catch(() => '');

    if (cssText) {
      const style = document.createElement('style');
      style.id = 'obsidian-theme-css';
      style.textContent = patchObsidianCSS(cssText);
      document.head.appendChild(style);
    }
  }

  let note: PubNoteDetail;
  try {
    note = await pubGetNote(repoId, notePath);
  } catch (e: unknown) {
    wrap.innerHTML = `
      <a href="#/read/${repoId}" style="color:#64748b;font-size:0.875rem;text-decoration:none">← Back</a>
      <p style="color:#c00;margin-top:16px">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const back = document.createElement('a');
  back.href = `#/read/${repoId}`;
  back.style.cssText = 'color:#64748b;font-size:0.875rem;text-decoration:none;display:block;margin-bottom:32px';
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
  date.style.cssText = 'font-size:0.8rem;color:#94a3b8';
  date.textContent = note.synced_at
    ? new Date(note.synced_at).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' })
    : '';
  meta.appendChild(date);

  for (const tag of note.tags ?? []) {
    const badge = document.createElement('span');
    badge.style.cssText =
      'font-size:0.7rem;padding:2px 8px;background:#f1f5f9;border-radius:999px;color:#475569';
    badge.textContent = tag;
    meta.appendChild(badge);
  }
  article.appendChild(meta);

  const content = document.createElement('div');
  content.className = 'markdown-rendered markdown-preview-view';
  content.innerHTML = note.html_content;
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
    section.style.cssText = 'margin-top:48px;padding-top:24px;border-top:1px solid #e2e8f0';

    const blTitle = document.createElement('h2');
    blTitle.style.cssText =
      'font-size:0.875rem;font-weight:600;color:#64748b;margin:0 0 12px;' +
      'text-transform:uppercase;letter-spacing:0.05em';
    blTitle.textContent = 'Linked from';
    section.appendChild(blTitle);

    const blList = document.createElement('ul');
    blList.style.cssText = 'list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:4px';
    for (const bl of note.backlinks) {
      const li = document.createElement('li');
      const a = document.createElement('a');
      a.href = `#/read/${repoId}/${bl.path}`;
      a.style.cssText = 'color:#0f172a;font-size:0.875rem';
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

function buildCommentsSection(repoId: string, notePath: string, note: PubNoteDetail): HTMLElement {
  const section = document.createElement('section');
  section.style.cssText = 'margin-top:48px;padding-top:24px;border-top:1px solid #e2e8f0';

  const h = document.createElement('h2');
  h.style.cssText = 'font-size:0.875rem;font-weight:600;color:#64748b;margin:0 0 16px;text-transform:uppercase;letter-spacing:0.05em';
  h.textContent = 'Comments';
  section.appendChild(h);

  const list = document.createElement('div');
  list.id = `comments-list-${note.id}`;
  list.textContent = 'Loading…';
  list.style.cssText = 'color:#94a3b8;font-size:0.875rem';
  section.appendChild(list);

  // Post form (authenticated) or sign-in prompt
  const formWrap = document.createElement('div');
  formWrap.style.marginTop = '20px';
  if (isAuthenticated()) {
    const ta = document.createElement('textarea');
    ta.placeholder = 'Write a comment…';
    ta.style.cssText = 'width:100%;padding:10px;border:1px solid #e2e8f0;border-radius:6px;font-size:0.875rem;resize:vertical;min-height:80px;font-family:inherit;box-sizing:border-box';
    formWrap.appendChild(ta);

    const row = document.createElement('div');
    row.style.cssText = 'display:flex;gap:8px;align-items:center;margin-top:8px';

    const btn = document.createElement('button');
    btn.textContent = 'Post comment';
    btn.style.cssText = 'padding:8px 16px;background:#0f172a;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem';
    row.appendChild(btn);

    const err = document.createElement('span');
    err.style.cssText = 'color:#c00;font-size:0.8rem';
    row.appendChild(err);
    formWrap.appendChild(row);

    btn.addEventListener('click', async () => {
      const body = ta.value.trim();
      if (!body) return;
      btn.disabled = true;
      err.textContent = '';
      try {
        await addComment(repoId, notePath, body);
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
    p.style.cssText = 'font-size:0.875rem;color:#64748b';
    p.innerHTML = `<a href="#/login" style="color:#2563eb">Sign in</a> to leave a comment.`;
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
    list.style.color = '#c00';
    return;
  }

  list.textContent = '';
  list.style.color = '';

  if (comments.length === 0) {
    list.style.cssText = 'color:#94a3b8;font-size:0.875rem';
    list.textContent = 'No comments yet.';
    return;
  }

  list.style.cssText = 'display:flex;flex-direction:column;gap:12px';
  for (const c of comments) {
    const card = document.createElement('div');
    card.style.cssText = 'padding:12px 16px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:6px';

    const meta = document.createElement('div');
    meta.style.cssText = 'display:flex;gap:8px;align-items:baseline;margin-bottom:6px';
    meta.innerHTML = `
      <span style="font-size:0.8rem;font-weight:600;color:#0f172a">${esc(c.author_name || c.author_email)}</span>
      <span style="font-size:0.75rem;color:#94a3b8">${new Date(c.created_at).toLocaleDateString(undefined, { year:'numeric', month:'short', day:'numeric' })}</span>
    `;

    const body = document.createElement('div');
    body.style.cssText = 'font-size:0.875rem;color:#1a1a1a';
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
