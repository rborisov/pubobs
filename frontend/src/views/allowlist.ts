import { listAllowlist, addAllowlistEntry, removeAllowlistEntry, type AllowlistEntry } from '../api';

export async function allowlistView(): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  const title = document.createElement('h2');
  title.style.cssText = 'margin:0 0 8px;font-size:1.25rem';
  title.textContent = 'Registration allowlist';
  wrap.appendChild(title);

  const hint = document.createElement('p');
  hint.style.cssText = 'margin:0 0 24px;color:#64748b;font-size:0.875rem';
  hint.textContent = 'When empty, registration is open to everyone. Add exact emails (user@example.com) or domain patterns (@example.com) to restrict who can sign up.';
  wrap.appendChild(hint);

  const listWrap = document.createElement('div');
  wrap.appendChild(listWrap);

  const addForm = buildAddForm(async (pattern) => {
    await addAllowlistEntry(pattern);
    await reload();
  });
  wrap.appendChild(addForm);

  async function reload(): Promise<void> {
    let entries: AllowlistEntry[];
    try {
      entries = await listAllowlist();
    } catch (e: unknown) {
      listWrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
      return;
    }
    renderList(listWrap, entries, async (id) => {
      await removeAllowlistEntry(id);
      await reload();
    });
  }

  await reload();
  return wrap;
}

function renderList(
  container: HTMLElement,
  entries: AllowlistEntry[],
  onRemove: (id: string) => Promise<void>,
): void {
  container.innerHTML = '';

  if (entries.length === 0) {
    const p = document.createElement('p');
    p.style.cssText = 'color:#64748b;font-style:italic;margin:0 0 16px';
    p.textContent = 'No entries — registration is open to everyone.';
    container.appendChild(p);
    return;
  }

  const table = document.createElement('table');
  table.style.marginBottom = '16px';
  table.innerHTML = `<thead><tr><th>Pattern</th><th>Added</th><th></th></tr></thead>`;
  const tbody = document.createElement('tbody');

  for (const entry of entries) {
    const row = document.createElement('tr');
    const patternCell = document.createElement('td');
    patternCell.style.fontFamily = 'monospace';
    patternCell.textContent = entry.pattern;

    const dateCell = document.createElement('td');
    dateCell.style.color = '#94a3b8';
    dateCell.textContent = new Date(entry.created_at).toLocaleDateString();

    const actionCell = document.createElement('td');
    const removeBtn = document.createElement('button');
    removeBtn.textContent = 'Remove';
    removeBtn.style.cssText =
      'background:none;border:none;cursor:pointer;color:#dc2626;text-decoration:underline;font-size:0.8rem;padding:0';
    removeBtn.addEventListener('click', async () => {
      if (!confirm(`Remove allowlist entry "${entry.pattern}"?`)) return;
      removeBtn.disabled = true;
      try {
        await onRemove(entry.id);
      } catch (e: unknown) {
        alert(e instanceof Error ? e.message : String(e));
        removeBtn.disabled = false;
      }
    });
    actionCell.appendChild(removeBtn);

    row.appendChild(patternCell);
    row.appendChild(dateCell);
    row.appendChild(actionCell);
    tbody.appendChild(row);
  }

  table.appendChild(tbody);
  container.appendChild(table);
}

function buildAddForm(onAdd: (pattern: string) => Promise<void>): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText =
    'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-top:8px';

  const heading = document.createElement('h3');
  heading.style.cssText = 'margin:0 0 12px;font-size:0.875rem;font-weight:600';
  heading.textContent = 'Add entry';
  wrap.appendChild(heading);

  const row = document.createElement('div');
  row.style.cssText = 'display:flex;gap:8px;align-items:flex-end;flex-wrap:wrap';

  const label = document.createElement('label');
  label.style.cssText = 'font-size:0.8rem;font-weight:500;color:#374151;flex:1;min-width:200px';
  label.textContent = 'Email or domain pattern';

  const input = document.createElement('input');
  input.type = 'text';
  input.placeholder = 'user@example.com or @example.com';
  input.style.cssText =
    'display:block;width:100%;margin-top:4px;padding:6px 10px;' +
    'border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem;font-family:monospace';
  label.appendChild(input);
  row.appendChild(label);

  const addBtn = document.createElement('button');
  addBtn.textContent = 'Add';
  addBtn.style.cssText =
    'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem;align-self:flex-end;margin-top:4px';

  const errSpan = document.createElement('span');
  errSpan.style.cssText = 'color:#c00;font-size:0.8rem;display:none;align-self:center';

  row.appendChild(addBtn);
  row.appendChild(errSpan);
  wrap.appendChild(row);

  addBtn.addEventListener('click', async () => {
    const pattern = input.value.trim();
    if (!pattern) return;
    errSpan.style.display = 'none';
    addBtn.disabled = true;
    addBtn.textContent = 'Adding…';
    try {
      await onAdd(pattern);
      input.value = '';
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'inline';
    } finally {
      addBtn.disabled = false;
      addBtn.textContent = 'Add';
    }
  });

  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') addBtn.click();
  });

  return wrap;
}
