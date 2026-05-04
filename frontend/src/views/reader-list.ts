import { pubListNotes, type PubNote, type PubRepo } from '../api';
import { ensureReaderStyles } from '../reader-styles';

export async function readerListView(repoId: string): Promise<HTMLElement> {
  ensureReaderStyles();

  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:720px;margin:0 auto;padding:40px 24px;font-family:system-ui,sans-serif';

  let data: { repo: PubRepo; notes: PubNote[] };
  try {
    data = await pubListNotes(repoId);
  } catch (e: unknown) {
    wrap.innerHTML = `<p class="r-error">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const { repo, notes } = data;

  const header = document.createElement('header');
  header.style.cssText = 'margin-bottom:40px;padding-bottom:24px';
  header.className = 'r-border-bottom';
  header.innerHTML = `<h1 style="margin:0 0 4px;font-size:1.75rem;font-weight:700">${esc(repo.name)}</h1>
    <p style="margin:0;font-size:0.875rem" class="r-muted">${notes.length} note${notes.length !== 1 ? 's' : ''}</p>`;
  wrap.appendChild(header);

  if (notes.length === 0) {
    const empty = document.createElement('p');
    empty.className = 'r-faint';
    empty.textContent = 'No notes published yet.';
    wrap.appendChild(empty);
    return wrap;
  }

  const list = document.createElement('ul');
  list.style.cssText = 'list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:2px';

  for (const note of notes) {
    const li = document.createElement('li');
    const a = document.createElement('a');
    a.href = `#/read/${repoId}/${note.path}`;
    a.className = 'r-note-link';

    const titleSpan = document.createElement('span');
    titleSpan.className = 'r-note-link-title';
    titleSpan.textContent = note.title;

    const dateSpan = document.createElement('span');
    dateSpan.className = 'r-note-link-date';
    dateSpan.textContent = note.synced_at ? new Date(note.synced_at).toLocaleDateString() : '';

    a.appendChild(titleSpan);
    a.appendChild(dateSpan);
    li.appendChild(a);
    list.appendChild(li);
  }

  wrap.appendChild(list);
  return wrap;
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
