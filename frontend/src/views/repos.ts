import { listRepos, createRepo, updateRepo, deleteRepo, importRepo, setRepoGuestAccess, type Repo } from '../api';

export async function reposView(_me?: import('../api').Me): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let repos: Repo[];
  try {
    repos = await listRepos();
  } catch (e: unknown) {
    wrap.appendChild(errEl(e));
    return wrap;
  }

  // Header row
  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;margin-bottom:24px';
  const title = document.createElement('h2');
  title.style.cssText = 'margin:0;font-size:1.25rem;flex:1';
  title.textContent = 'Repos';
  header.appendChild(title);
  const newBtn = mkBtn('+ New repo', 'primary');
  header.appendChild(newBtn);
  wrap.appendChild(header);

  // Table
  const tableWrap = document.createElement('div');
  wrap.appendChild(tableWrap);
  renderTable(tableWrap, repos);

  // Create form area
  const formWrap = document.createElement('div');
  wrap.appendChild(formWrap);

  newBtn.addEventListener('click', () => {
    if (formWrap.firstChild) {
      formWrap.innerHTML = '';
      return;
    }
    formWrap.appendChild(
      repoForm(null, async data => {
        await createRepo(data);
        repos = await listRepos();
        renderTable(tableWrap, repos);
        formWrap.innerHTML = '';
      }, () => { formWrap.innerHTML = ''; })
    );
  });

  return wrap;
}

function renderTable(container: HTMLElement, repos: Repo[]): void {
  container.innerHTML = '';

  if (repos.length === 0) {
    const p = document.createElement('p');
    p.style.color = '#888';
    p.textContent = 'No repos yet. Click "+ New repo" to create one.';
    container.appendChild(p);
    return;
  }

  const table = document.createElement('table');
  table.innerHTML = `<thead><tr>
    <th>Name</th><th>Remote</th><th>Branch</th><th>Status</th><th>Guest</th><th></th>
  </tr></thead>`;

  const tbody = document.createElement('tbody');

  for (const repo of repos) {
    const row = document.createElement('tr');
    row.dataset['id'] = repo.id;

    const nameCell = document.createElement('td');
    const link = document.createElement('a');
    link.href = `#/repos/${repo.id}`;
    link.style.fontWeight = '500';
    link.textContent = repo.name;
    nameCell.appendChild(link);

    const remoteCell = document.createElement('td');
    remoteCell.style.cssText = 'color:#555;font-size:0.8rem;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
    remoteCell.title = repo.remote_url;
    remoteCell.textContent = repo.remote_url;

    const branchCell = document.createElement('td');
    branchCell.textContent = repo.default_branch;

    const statusCell = document.createElement('td');
    statusCell.textContent = repo.is_cloned ? '● cloned' : '○ pending';
    statusCell.style.color = repo.is_cloned ? '#16a34a' : '#94a3b8';

    const guestCell = document.createElement('td');
    const guestBtn = mkBtn(repo.allow_guest ? 'On' : 'Off', repo.allow_guest ? 'toggle-on' : 'toggle-off');
    guestCell.appendChild(guestBtn);

    const actionsCell = document.createElement('td');
    actionsCell.style.whiteSpace = 'nowrap';

    const editBtn = mkBtn('Edit', 'link');
    editBtn.style.marginRight = '8px';
    const importBtn = mkBtn('Import', 'link');
    importBtn.style.marginRight = '8px';
    const delBtn = mkBtn('Delete', 'link-danger');
    actionsCell.appendChild(editBtn);
    actionsCell.appendChild(importBtn);
    actionsCell.appendChild(delBtn);

    row.appendChild(nameCell);
    row.appendChild(remoteCell);
    row.appendChild(branchCell);
    row.appendChild(statusCell);
    row.appendChild(guestCell);
    row.appendChild(actionsCell);
    tbody.appendChild(row);

    // Inline edit
    editBtn.addEventListener('click', () => {
      // Remove any existing inline form in the table
      const existing = tbody.querySelector('tr.inline-form');
      if (existing) existing.remove();
      if (row.nextSibling && (row.nextSibling as HTMLElement).classList?.contains('inline-form')) return;

      const formRow = document.createElement('tr');
      formRow.className = 'inline-form';
      const formCell = document.createElement('td');
      formCell.colSpan = 6;
      formCell.style.padding = '0';

      formCell.appendChild(
        repoForm(repo, async data => {
          await updateRepo(repo.id, data);
          const fresh = await listRepos();
          renderTable(container, fresh);
        }, () => { formRow.remove(); })
      );

      formRow.appendChild(formCell);
      row.after(formRow);
    });

    // Toggle guest access
    guestBtn.addEventListener('click', async () => {
      const next = !repo.allow_guest;
      guestBtn.disabled = true;
      try {
        await setRepoGuestAccess(repo.id, next);
        repo.allow_guest = next;
        guestBtn.textContent = next ? 'On' : 'Off';
        applyToggleStyle(guestBtn, next);
      } catch (e: unknown) {
        alert(e instanceof Error ? e.message : String(e));
      } finally {
        guestBtn.disabled = false;
      }
    });

    // Import from git
    importBtn.addEventListener('click', async () => {
      importBtn.textContent = 'Importing…';
      importBtn.disabled = true;
      try {
        const { imported } = await importRepo(repo.id);
        alert(`Imported ${imported} note(s) from git.`);
      } catch (e: unknown) {
        alert(e instanceof Error ? e.message : String(e));
      } finally {
        importBtn.textContent = 'Import';
        importBtn.disabled = false;
      }
    });

    // Delete
    delBtn.addEventListener('click', async () => {
      if (!confirm(`Delete repo "${repo.name}"?\n\nThis removes the repo from PubObs (the remote git repo is not affected).`)) return;
      try {
        await deleteRepo(repo.id);
        const fresh = await listRepos();
        renderTable(container, fresh);
      } catch (e: unknown) {
        alert(e instanceof Error ? e.message : String(e));
      }
    });
  }

  table.appendChild(tbody);
  container.appendChild(table);
}

type RepoFormData = {
  name: string;
  remote_url: string;
  default_branch: string;
  username: string;
  password: string;
};

function repoForm(
  existing: Repo | null,
  onSave: (data: RepoFormData) => Promise<void>,
  onCancel: () => void,
): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText =
    'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:20px;margin:12px 0';

  const heading = document.createElement('h3');
  heading.style.cssText = 'margin:0 0 16px;font-size:1rem;font-family:system-ui,sans-serif';
  heading.textContent = existing ? 'Edit repo' : 'New repo';
  wrap.appendChild(heading);

  const grid = document.createElement('div');
  grid.style.cssText = 'display:grid;grid-template-columns:1fr 1fr;gap:12px';

  const fields: Array<{ name: string; label: string; placeholder: string; type?: string; value?: string }> = [
    { name: 'name',           label: 'Name',              placeholder: 'my-blog',                          value: existing?.name ?? '' },
    { name: 'remote_url',     label: 'Remote URL',        placeholder: 'https://github.com/user/repo.git', value: existing?.remote_url ?? '' },
    { name: 'default_branch', label: 'Default branch',    placeholder: 'main',                             value: existing?.default_branch ?? 'main' },
    { name: '_spacer',        label: '',                  placeholder: '' },
    { name: 'username',       label: 'Git username',      placeholder: existing ? 'leave blank to keep' : '' },
    { name: 'password',       label: 'Password / token',  placeholder: existing ? 'leave blank to keep' : '', type: 'password' },
  ];

  for (const f of fields) {
    if (f.name === '_spacer') { grid.appendChild(document.createElement('div')); continue; }
    const label = document.createElement('label');
    label.style.cssText = 'font-family:system-ui,sans-serif;font-size:0.8rem;font-weight:500;color:#374151';
    label.textContent = f.label;
    const input = document.createElement('input');
    input.name = f.name;
    input.type = f.type ?? 'text';
    input.placeholder = f.placeholder;
    input.value = f.value ?? '';
    input.style.cssText =
      'display:block;width:100%;margin-top:4px;padding:6px 10px;' +
      'border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem';
    label.appendChild(input);
    grid.appendChild(label);
  }
  wrap.appendChild(grid);

  const footer = document.createElement('div');
  footer.style.cssText = 'margin-top:16px;display:flex;gap:8px;align-items:center';

  const saveBtn = mkBtn('Save', 'primary');
  const cancelBtn = mkBtn('Cancel', 'ghost');
  const errSpan = document.createElement('span');
  errSpan.style.cssText = 'color:#c00;font-size:0.8rem;display:none';

  footer.appendChild(saveBtn);
  footer.appendChild(cancelBtn);
  footer.appendChild(errSpan);
  wrap.appendChild(footer);

  cancelBtn.addEventListener('click', onCancel);

  saveBtn.addEventListener('click', async () => {
    const val = (n: string) =>
      (wrap.querySelector(`input[name="${n}"]`) as HTMLInputElement).value.trim();
    errSpan.style.display = 'none';
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    try {
      await onSave({
        name: val('name'),
        remote_url: val('remote_url'),
        default_branch: val('default_branch'),
        username: val('username'),
        password: val('password'),
      });
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'inline';
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  return wrap;
}

function applyToggleStyle(b: HTMLButtonElement, on: boolean): void {
  b.style.cssText = on
    ? 'padding:2px 8px;background:#d1fae5;color:#065f46;border:1px solid #6ee7b7;border-radius:4px;cursor:pointer;font-size:0.75rem'
    : 'padding:2px 8px;background:#f1f5f9;color:#64748b;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;font-size:0.75rem';
}

function mkBtn(text: string, variant: 'primary' | 'ghost' | 'link' | 'link-danger' | 'toggle-on' | 'toggle-off'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  if (variant === 'toggle-on' || variant === 'toggle-off') {
    applyToggleStyle(b, variant === 'toggle-on');
    return b;
  }
  const styles: Record<string, string> = {
    primary:
      'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem',
    ghost:
      'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer;font-size:0.875rem',
    link:
      'background:none;border:none;cursor:pointer;color:#5B6B8E;text-decoration:underline;font-size:0.8rem;padding:0',
    'link-danger':
      'background:none;border:none;cursor:pointer;color:#dc2626;text-decoration:underline;font-size:0.8rem;padding:0',
  };
  b.style.cssText = styles[variant];
  return b;
}

function errEl(e: unknown): HTMLElement {
  const p = document.createElement('p');
  p.style.cssText = 'color:#c00;font-family:system-ui,sans-serif';
  p.textContent = e instanceof Error ? e.message : String(e);
  return p;
}
