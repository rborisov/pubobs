import { pubListNotes, type PubNote, type PubRepo } from '../api';
import { ensureReaderStyles } from '../reader-styles';

interface ViewState {
  folder: string;     // "" = all notes
  tag: string | null; // null = no tag filter
  query: string;
  openFolders: Set<string>;
}

interface FolderNode {
  name: string;
  fullPath: string;
  children: FolderNode[];
  count: number; // total notes (unfiltered) under this node
}

function dirOf(path: string): string {
  const i = path.lastIndexOf('/');
  return i === -1 ? '' : path.slice(0, i);
}

function buildFolderTree(notes: PubNote[]): FolderNode {
  const root: FolderNode = { name: '', fullPath: '', children: [], count: notes.length };
  const byPath = new Map<string, FolderNode>();
  byPath.set('', root);

  function ensure(folderPath: string): FolderNode {
    if (byPath.has(folderPath)) return byPath.get(folderPath)!;
    const parentPath = dirOf(folderPath);
    const parent = ensure(parentPath);
    const name = folderPath.slice(parentPath ? parentPath.length + 1 : 0);
    const node: FolderNode = { name, fullPath: folderPath, children: [], count: 0 };
    parent.children.push(node);
    byPath.set(folderPath, node);
    return node;
  }

  for (const n of notes) {
    const folder = dirOf(n.path);
    if (folder) {
      // Increment count up the ancestor chain
      const parts = folder.split('/');
      for (let i = 1; i <= parts.length; i++) {
        const ancestor = parts.slice(0, i).join('/');
        ensure(ancestor).count++;
      }
    }
  }

  return root;
}

function collectTags(notes: PubNote[]): string[] {
  const set = new Set<string>();
  for (const n of notes) for (const t of n.tags ?? []) set.add(t);
  return [...set].sort();
}

function filterNotes(notes: PubNote[], state: ViewState): PubNote[] {
  const q = state.query.toLowerCase();
  return notes.filter(n => {
    if (state.folder && n.path !== state.folder &&
        !n.path.startsWith(state.folder + '/')) return false;
    if (state.tag && !(n.tags ?? []).includes(state.tag)) return false;
    if (q && !n.title.toLowerCase().includes(q) && !n.path.toLowerCase().includes(q)) return false;
    return true;
  });
}

function renderFolderTree(
  node: FolderNode,
  state: ViewState,
  onSelect: (folder: string) => void,
  depth = 0,
): HTMLElement {
  const wrap = document.createElement('div');

  if (depth === 0) {
    // "All notes" entry
    const all = document.createElement('div');
    all.style.cssText = `
      padding:5px 8px 5px ${8 + depth * 16}px;
      border-radius:4px;cursor:pointer;font-size:0.85rem;
      display:flex;align-items:center;justify-content:space-between;
      ${state.folder === '' ? 'background:var(--r-hover-bg);font-weight:600' : ''}
    `;
    all.innerHTML = `<span>All notes</span><span style="color:var(--r-text-faint);font-size:0.75rem">${node.count}</span>`;
    all.addEventListener('click', () => onSelect(''));
    wrap.appendChild(all);
  }

  for (const child of node.children) {
    const isSelected = state.folder === child.fullPath;
    const hasChildren = child.children.length > 0;

    const row = document.createElement('div');
    row.style.cssText = `
      padding:5px 8px 5px ${8 + (depth + 1) * 14}px;
      border-radius:4px;cursor:pointer;font-size:0.85rem;
      display:flex;align-items:center;gap:4px;
      ${isSelected ? 'background:var(--r-hover-bg);font-weight:600' : ''}
    `;
    row.dataset['path'] = child.fullPath;

    const toggle = document.createElement('span');
    toggle.style.cssText = 'width:12px;flex-shrink:0;color:var(--r-text-faint);font-size:0.7rem;user-select:none';
    toggle.textContent = hasChildren ? '▸' : '';

    const label = document.createElement('span');
    label.style.flex = '1';
    label.textContent = child.name;

    const count = document.createElement('span');
    count.style.cssText = 'color:var(--r-text-faint);font-size:0.75rem;margin-left:4px';
    count.textContent = String(child.count);

    row.appendChild(toggle);
    row.appendChild(label);
    row.appendChild(count);

    let childrenEl: HTMLElement | null = null;
    if (hasChildren) {
      childrenEl = renderFolderTree(child, state, onSelect, depth + 1);
      const isOpen = state.openFolders.has(child.fullPath);
      childrenEl.style.display = isOpen ? '' : 'none';
      toggle.textContent = isOpen ? '▾' : '▸';

      toggle.style.cursor = 'pointer';
      toggle.addEventListener('click', (e) => {
        e.stopPropagation();
        const open = state.openFolders.has(child.fullPath);
        if (open) {
          state.openFolders.delete(child.fullPath);
          childrenEl!.style.display = 'none';
          toggle.textContent = '▸';
        } else {
          state.openFolders.add(child.fullPath);
          childrenEl!.style.display = '';
          toggle.textContent = '▾';
        }
      });
    }

    row.addEventListener('click', () => {
      if (hasChildren) {
        state.openFolders.add(child.fullPath);
        if (childrenEl) {
          childrenEl.style.display = '';
          toggle.textContent = '▾';
        }
      }
      onSelect(child.fullPath);
    });

    wrap.appendChild(row);
    if (childrenEl) wrap.appendChild(childrenEl);
  }

  return wrap;
}

function renderTagList(
  tags: string[],
  state: ViewState,
  onSelect: (tag: string | null) => void,
): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'display:flex;flex-wrap:wrap;gap:6px;padding:4px 0';

  for (const tag of tags) {
    const chip = document.createElement('button');
    const active = state.tag === tag;
    chip.textContent = tag;
    chip.style.cssText = `
      padding:3px 10px;border-radius:999px;font-size:0.75rem;cursor:pointer;border:none;
      ${active
        ? 'background:#5B6B8E;color:#fff'
        : 'background:var(--r-tag-bg);color:var(--r-tag-text)'}
    `;
    chip.addEventListener('click', () => onSelect(active ? null : tag));
    wrap.appendChild(chip);
  }

  if (tags.length === 0) {
    const p = document.createElement('p');
    p.style.cssText = 'margin:0;font-size:0.8rem;color:var(--r-text-faint)';
    p.textContent = 'No tags';
    wrap.appendChild(p);
  }

  return wrap;
}

function renderNoteList(
  container: HTMLElement,
  notes: PubNote[],
  repoId: string,
  state: ViewState,
): void {
  container.innerHTML = '';

  if (notes.length === 0) {
    const p = document.createElement('p');
    p.className = 'r-faint';
    p.style.cssText = 'margin:32px 0;text-align:center;font-size:0.9rem';
    p.textContent = 'No notes match the current filter.';
    container.appendChild(p);
    return;
  }

  // Group by folder
  const groups = new Map<string, PubNote[]>();
  for (const n of notes) {
    const folder = dirOf(n.path);
    if (!groups.has(folder)) groups.set(folder, []);
    groups.get(folder)!.push(n);
  }

  for (const [folder, items] of groups) {
    if (folder && !state.folder) {
      // Show folder heading only when not already filtered to a single folder
      const heading = document.createElement('div');
      heading.style.cssText =
        'margin:24px 0 8px;font-size:0.75rem;font-weight:600;color:var(--r-text-muted);' +
        'text-transform:uppercase;letter-spacing:0.06em;padding-bottom:6px;border-bottom:1px solid var(--r-border)';
      heading.textContent = folder;
      container.appendChild(heading);
    } else if (!folder && !state.folder && groups.size > 1) {
      const heading = document.createElement('div');
      heading.style.cssText =
        'margin:24px 0 8px;font-size:0.75rem;font-weight:600;color:var(--r-text-muted);' +
        'text-transform:uppercase;letter-spacing:0.06em;padding-bottom:6px;border-bottom:1px solid var(--r-border)';
      heading.textContent = '/';
      container.appendChild(heading);
    }

    for (const note of items) {
      const a = document.createElement('a');
      a.href = `#/read/${repoId}/${note.path}`;
      a.className = 'r-note-link';

      const left = document.createElement('div');
      left.style.cssText = 'display:flex;flex-direction:column;gap:4px;min-width:0';

      const titleSpan = document.createElement('span');
      titleSpan.className = 'r-note-link-title';
      titleSpan.textContent = note.title;
      left.appendChild(titleSpan);

      if ((note.tags ?? []).length > 0) {
        const tagRow = document.createElement('div');
        tagRow.style.cssText = 'display:flex;gap:4px;flex-wrap:wrap';
        for (const tag of note.tags) {
          const t = document.createElement('span');
          t.className = 'r-tag';
          t.textContent = tag;
          tagRow.appendChild(t);
        }
        left.appendChild(tagRow);
      }

      const dateSpan = document.createElement('span');
      dateSpan.className = 'r-note-link-date';
      dateSpan.textContent = note.synced_at
        ? new Date(note.synced_at).toLocaleDateString() : '';

      a.appendChild(left);
      a.appendChild(dateSpan);
      container.appendChild(a);
    }
  }
}

export async function readerListView(repoId: string): Promise<HTMLElement> {
  ensureReaderStyles();

  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:1100px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let data: { repo: PubRepo; notes: PubNote[] };
  try {
    data = await pubListNotes(repoId);
  } catch (e: unknown) {
    wrap.innerHTML = `<p class="r-error">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const { repo, notes } = data;
  const state: ViewState = { folder: '', tag: null, query: '', openFolders: new Set() };
  const allTags = collectTags(notes);
  const folderTree = buildFolderTree(notes);

  // Header
  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:baseline;gap:16px;margin-bottom:28px';
  header.innerHTML = `
    <a href="#/dashboard" style="font-size:0.875rem;text-decoration:none" class="r-muted">← My repos</a>
    <h1 style="margin:0;font-size:1.5rem;font-weight:700">${esc(repo.name)}</h1>
  `;
  wrap.appendChild(header);

  // Body: sidebar + main
  const body = document.createElement('div');
  body.style.cssText = 'display:flex;gap:24px;align-items:flex-start';

  // Sidebar
  const sidebar = document.createElement('div');
  sidebar.style.cssText =
    'width:220px;flex-shrink:0;position:sticky;top:24px;max-height:calc(100vh - 80px);' +
    'overflow-y:auto;display:flex;flex-direction:column;gap:20px';

  // Search
  const searchWrap = document.createElement('div');
  const searchInput = document.createElement('input');
  searchInput.type = 'search';
  searchInput.placeholder = 'Search notes…';
  searchInput.style.cssText =
    'width:100%;padding:7px 10px;border:1px solid var(--r-border);border-radius:6px;' +
    'font-size:0.85rem;background:var(--r-bg);color:var(--r-text);box-sizing:border-box';
  searchWrap.appendChild(searchInput);
  sidebar.appendChild(searchWrap);

  // Folder section
  const folderSection = document.createElement('div');
  const folderHeading = document.createElement('div');
  folderHeading.className = 'r-section-heading';
  folderHeading.textContent = 'Folders';
  folderSection.appendChild(folderHeading);
  const folderTreeEl = document.createElement('div');
  folderSection.appendChild(folderTreeEl);
  sidebar.appendChild(folderSection);

  // Tags section
  const tagSection = document.createElement('div');
  const tagHeading = document.createElement('div');
  tagHeading.className = 'r-section-heading';
  tagHeading.textContent = 'Tags';
  tagSection.appendChild(tagHeading);
  const tagListEl = document.createElement('div');
  tagSection.appendChild(tagListEl);
  if (allTags.length > 0) sidebar.appendChild(tagSection);

  // Main area
  const main = document.createElement('div');
  main.style.cssText = 'flex:1;min-width:0';

  const filterBar = document.createElement('div');
  filterBar.style.cssText =
    'font-size:0.8rem;color:var(--r-text-muted);margin-bottom:12px;min-height:1.2em';
  main.appendChild(filterBar);

  const noteListEl = document.createElement('div');
  main.appendChild(noteListEl);

  body.appendChild(sidebar);
  body.appendChild(main);
  wrap.appendChild(body);

  function refresh(): void {
    const filtered = filterNotes(notes, state);

    // Filter bar label
    const parts: string[] = [];
    if (state.folder) parts.push(`📁 ${state.folder}`);
    if (state.tag) parts.push(`🏷 ${state.tag}`);
    if (state.query) parts.push(`"${state.query}"`);
    filterBar.textContent = parts.length
      ? `${filtered.length} note${filtered.length !== 1 ? 's' : ''} — ${parts.join(' · ')}`
      : `${filtered.length} note${filtered.length !== 1 ? 's' : ''}`;

    // Folder tree (rebuild to update active state)
    folderTreeEl.innerHTML = '';
    folderTreeEl.appendChild(renderFolderTree(folderTree, state, folder => {
      state.folder = folder;
      refresh();
    }));

    // Tag list (rebuild to update active state)
    tagListEl.innerHTML = '';
    tagListEl.appendChild(renderTagList(allTags, state, tag => {
      state.tag = tag;
      refresh();
    }));

    // Note list
    renderNoteList(noteListEl, filtered, repoId, state);
  }

  searchInput.addEventListener('input', () => {
    state.query = searchInput.value.trim();
    refresh();
  });

  refresh();
  return wrap;
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
