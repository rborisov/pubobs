import {
  listRepos, listRepoAccess, grantAccess, revokeAccess, listUsers,
  updateRepo, deleteRepo, listStorageDestinations, assignRepoStorage,
  setRepoOwner, setRepoStrictCredentials,
  type Repo, type RepoAccess, type User, type StorageDestination,
} from '../api';
import { navigate } from '../router';

export async function repoDetailView(id: string): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let repo: Repo | undefined;
  let accessList: RepoAccess[];
  let users: User[];
  let destinations: StorageDestination[];

  try {
    const [repos, access, allUsers, dests] = await Promise.all([
      listRepos(), listRepoAccess(id), listUsers(), listStorageDestinations(),
    ]);
    repo = repos.find(r => r.id === id);
    accessList = access;
    users = allUsers;
    destinations = dests;
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  if (!repo) {
    wrap.innerHTML = `<p style="color:#c00">Repo not found.</p>`;
    return wrap;
  }

  render(wrap, repo, accessList, users, destinations);
  return wrap;
}

function render(wrap: HTMLElement, repo: Repo, accessList: RepoAccess[], users: User[], destinations: StorageDestination[]): void {
  wrap.innerHTML = '';

  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:24px';
  header.innerHTML = `
    <a href="#/repos" style="color:#64748b;text-decoration:none;font-size:0.875rem">← Repos</a>
    <h2 style="margin:0;font-size:1.25rem;flex:1">${esc(repo.name)}</h2>
  `;
  const editBtn = mkBtn('Edit', 'ghost');
  const delBtn = mkBtn('Delete', 'danger');
  header.appendChild(editBtn);
  header.appendChild(delBtn);
  wrap.appendChild(header);

  const card = document.createElement('div');
  card.style.cssText = 'background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:24px;font-size:0.875rem';
  card.innerHTML = `
    <div style="display:grid;grid-template-columns:auto 1fr;gap:6px 16px;color:#475569">
      <span style="font-weight:600">Remote</span><span>${esc(repo.remote_url)}</span>
      <span style="font-weight:600">Branch</span><span>${esc(repo.default_branch)}</span>
      <span style="font-weight:600" title="Whether this server currently has a local working copy of the repo (created automatically the first time it's synced, browsed, or imported).">Status</span><span>${repo.is_cloned ? '● cloned' : '○ not cloned yet'}</span>
    </div>
  `;
  wrap.appendChild(card);

  const storageSection = document.createElement('div');
  storageSection.style.cssText = 'background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:24px;font-size:0.875rem';
  wrap.appendChild(storageSection);

  const renderStorage = (): void => {
    storageSection.innerHTML = '';

    const row = document.createElement('label');
    row.style.cssText = 'display:flex;align-items:center;gap:8px;font-weight:600;color:#475569';
    row.textContent = 'Storage';

    const select = document.createElement('select');
    select.style.cssText = 'padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem;font-weight:normal';
    const localOpt = document.createElement('option');
    localOpt.value = '';
    localOpt.textContent = 'Local';
    select.appendChild(localOpt);
    for (const dest of destinations) {
      const opt = document.createElement('option');
      opt.value = dest.id;
      opt.textContent = dest.name;
      select.appendChild(opt);
    }
    select.value = repo.storage_destination_id ?? '';
    select.disabled = repo.migration_status === 'running';
    row.appendChild(select);
    storageSection.appendChild(row);

    const errEl = document.createElement('div');
    errEl.style.cssText = 'color:#c00;font-size:0.8rem;display:none;margin-top:8px';
    storageSection.appendChild(errEl);

    if (repo.migration_status === 'running') {
      const status = document.createElement('div');
      status.style.cssText = 'color:#64748b;margin-top:8px';
      status.textContent = 'Migrating existing data to the new destination…';
      storageSection.appendChild(status);
    } else if (repo.migration_status === 'failed') {
      const status = document.createElement('div');
      status.style.cssText = 'color:#c00;margin-top:8px';
      status.textContent = 'Last migration failed — check server logs; data may be split across destinations.';
      storageSection.appendChild(status);
    }

    select.addEventListener('change', async () => {
      const newDestId = select.value || null;
      const currentDestId = repo.storage_destination_id ?? null;
      if (newDestId !== currentDestId) {
        const destName = newDestId ? (destinations.find(d => d.id === newDestId)?.name ?? 'the selected destination') : 'Local';
        const confirmed = confirm(
          `Switch this repo's storage to "${destName}"? Its existing renders and assets will be migrated from the current destination to the new one in the background — this may take a while for large repos.`,
        );
        if (!confirmed) {
          select.value = currentDestId ?? '';
          return;
        }
      }
      select.disabled = true;
      errEl.style.display = 'none';
      try {
        await assignRepoStorage(repo.id, newDestId);
        repo.storage_destination_id = newDestId;
        repo.migration_status = 'running';
        renderStorage();
        void pollMigration();
      } catch (e: unknown) {
        errEl.textContent = e instanceof Error ? e.message : String(e);
        errEl.style.display = 'block';
        select.disabled = false;
      }
    });
  };

  let migrationPolling = false;
  const pollMigration = async (): Promise<void> => {
    if (migrationPolling) return;
    migrationPolling = true;
    try {
      // Per-repo migrations finish in seconds; poll briefly until the repo
      // leaves "running", then re-render so the "Migrating…" message clears
      // itself without a manual page reload. Stops if the view navigates away
      // (storageSection detached) or after a ~60s safety cap.
      for (let i = 0; i < 40; i++) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        if (!storageSection.isConnected) return;
        let latest: Repo | undefined;
        try {
          latest = (await listRepos()).find((r) => r.id === repo.id);
        } catch {
          continue; // transient error — keep polling
        }
        if (!latest) return;
        if (latest.migration_status !== 'running') {
          repo.migration_status = latest.migration_status;
          repo.storage_destination_id = latest.storage_destination_id;
          renderStorage();
          return;
        }
      }
    } finally {
      migrationPolling = false;
    }
  };

  renderStorage();
  if (repo.migration_status === 'running') void pollMigration();

  const ownerSection = document.createElement('div');
  ownerSection.style.cssText = 'background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:24px;font-size:0.875rem';
  wrap.appendChild(ownerSection);

  const renderOwner = (): void => {
    ownerSection.innerHTML = '';

    const ownerUser = repo.owner_user_id ? users.find(u => u.id === repo.owner_user_id) : undefined;
    const ownerRow = document.createElement('div');
    ownerRow.style.cssText = 'display:grid;grid-template-columns:auto 1fr;gap:6px 16px;color:#475569;margin-bottom:12px';
    ownerRow.innerHTML = `<span style="font-weight:600">Owner</span><span>${esc(ownerUser?.email ?? '—')}</span>`;
    ownerSection.appendChild(ownerRow);

    const adminUsers = users.filter(u => u.is_instance_admin || u.is_admin);
    const transferRow = document.createElement('div');
    transferRow.style.cssText = 'display:flex;align-items:center;gap:8px;flex-wrap:wrap';

    const transferLabel = document.createElement('span');
    transferLabel.style.cssText = 'font-size:0.8rem;color:#475569';
    transferLabel.textContent = 'Transfer ownership';
    transferRow.appendChild(transferLabel);

    const select = document.createElement('select');
    select.style.cssText = 'padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem';
    for (const u of adminUsers) {
      const opt = document.createElement('option');
      opt.value = u.id;
      opt.textContent = u.email;
      select.appendChild(opt);
    }
    if (repo.owner_user_id) select.value = repo.owner_user_id;
    transferRow.appendChild(select);

    const transferBtn = mkBtn('Transfer', 'ghost');
    if (adminUsers.length === 0) transferBtn.disabled = true;
    transferRow.appendChild(transferBtn);
    ownerSection.appendChild(transferRow);

    const transferErr = document.createElement('div');
    transferErr.style.cssText = 'color:#c00;font-size:0.8rem;display:none;margin-top:8px';
    ownerSection.appendChild(transferErr);

    transferBtn.addEventListener('click', async () => {
      const selected = select.value;
      if (!selected) return;
      transferErr.style.display = 'none';
      transferBtn.disabled = true;
      try {
        await setRepoOwner(repo.id, selected);
        const repos = await listRepos();
        const fresh = repos.find(r => r.id === repo.id);
        if (fresh) Object.assign(repo, fresh);
        renderOwner();
      } catch (e: unknown) {
        transferErr.textContent = e instanceof Error ? e.message : String(e);
        transferErr.style.display = 'block';
        transferBtn.disabled = false;
      }
    });

    const strictRow = document.createElement('label');
    strictRow.style.cssText = 'display:flex;align-items:center;gap:8px;margin-top:16px;font-weight:600;color:#475569;cursor:pointer';
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.checked = !!repo.strict_credentials;
    strictRow.appendChild(checkbox);
    const strictLabel = document.createElement('span');
    strictLabel.textContent = "Require each editor's own git credential (strict)";
    strictRow.appendChild(strictLabel);
    ownerSection.appendChild(strictRow);

    const strictNote = document.createElement('div');
    strictNote.style.cssText = 'color:#64748b;font-size:0.8rem;margin-top:4px';
    strictNote.textContent = "Editors must configure a git credential in the plugin before they can publish once strict mode is on.";
    ownerSection.appendChild(strictNote);

    const strictErr = document.createElement('div');
    strictErr.style.cssText = 'color:#c00;font-size:0.8rem;display:none;margin-top:8px';
    ownerSection.appendChild(strictErr);

    checkbox.addEventListener('change', async () => {
      const newVal = checkbox.checked;
      checkbox.disabled = true;
      strictErr.style.display = 'none';
      try {
        await setRepoStrictCredentials(repo.id, newVal);
        repo.strict_credentials = newVal;
      } catch (e: unknown) {
        checkbox.checked = !newVal;
        strictErr.textContent = e instanceof Error ? e.message : String(e);
        strictErr.style.display = 'block';
      } finally {
        checkbox.disabled = false;
      }
    });
  };

  renderOwner();

  const editWrap = document.createElement('div');
  wrap.appendChild(editWrap);

  const openEditForm = (): void => {
    editWrap.innerHTML = '';
    editWrap.appendChild(repoEditForm(repo, async (data) => {
      await updateRepo(repo.id, data);
      const repos = await listRepos();
      const fresh = repos.find(r => r.id === repo.id);
      if (fresh) Object.assign(repo, fresh);
      editWrap.innerHTML = '';
      render(wrap, repo, accessList, users, destinations);
    }, () => { editWrap.innerHTML = ''; }));
  };

  editBtn.addEventListener('click', () => {
    if (editWrap.firstChild) { editWrap.innerHTML = ''; return; }
    openEditForm();
  });

  // This page is reached via the repos list's "Edit" link, so surface the
  // connection-settings form immediately alongside storage and access below.
  openEditForm();

  delBtn.addEventListener('click', async () => {
    if (!confirm(`Delete repo "${repo.name}"? This cannot be undone.`)) return;
    try {
      await deleteRepo(repo.id);
      navigate('/repos');
    } catch (e: unknown) { alert(e instanceof Error ? e.message : String(e)); }
  });

  const accessSection = document.createElement('div');
  wrap.appendChild(accessSection);

  const renderAccess = (list: RepoAccess[]) => {
    accessSection.innerHTML = '';

    const h = document.createElement('h3');
    h.style.cssText = 'font-size:1rem;margin:0 0 12px';
    h.textContent = 'Access';
    accessSection.appendChild(h);

    if (list.length === 0) {
      const p = document.createElement('p');
      p.style.color = '#888';
      p.textContent = 'No access entries.';
      accessSection.appendChild(p);
    } else {
      const table = document.createElement('table');
      table.innerHTML = `<thead><tr><th>Type</th><th>User</th><th>Role</th><th>Git credential</th><th></th></tr></thead>`;
      const tbody = document.createElement('tbody');
      for (const entry of list) {
        const user = users.find(u => u.id === entry.principal_id);
        const row = document.createElement('tr');
        row.innerHTML = `
          <td>${esc(entry.principal_type)}</td>
          <td>${esc(user?.email ?? entry.principal_id)}</td>
          <td>${esc(entry.role)}</td>
          <td>${entry.principal_type === 'user' ? esc(credentialStatusLabel(entry.git_credential)) : ''}</td>
          <td></td>
        `;
        const revokeBtn = mkBtn('Revoke', 'danger-sm');
        revokeBtn.addEventListener('click', async () => {
          if (!confirm(`Revoke access for ${user?.email ?? entry.principal_id}?`)) return;
          try {
            await revokeAccess(repo.id, entry.id);
            accessList = accessList.filter(a => a.id !== entry.id);
            renderAccess(accessList);
          } catch (e: unknown) { alert(e instanceof Error ? e.message : String(e)); }
        });
        row.querySelector('td:last-child')!.appendChild(revokeBtn);
        tbody.appendChild(row);
      }
      table.appendChild(tbody);
      accessSection.appendChild(table);
    }

    accessSection.appendChild(grantForm(users, async (userId, role) => {
      await grantAccess(repo.id, userId, role);
      accessList = await listRepoAccess(repo.id);
      renderAccess(accessList);
    }));
  };

  renderAccess(accessList);
}

function grantForm(users: User[], onGrant: (userId: string, role: string) => Promise<void>): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'margin-top:20px;background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:16px';
  const sel = 'display:block;margin-top:4px;padding:6px;border:1px solid #cbd5e1;border-radius:4px';
  wrap.innerHTML = `
    <h4 style="margin:0 0 12px;font-size:0.875rem;font-weight:600">Grant access</h4>
    <div style="display:flex;gap:8px;align-items:flex-end;flex-wrap:wrap">
      <label style="font-size:0.8rem">User
        <select name="user" style="${sel}">
          ${users.map(u => `<option value="${esc(u.id)}">${esc(u.email)}</option>`).join('')}
        </select>
      </label>
      <label style="font-size:0.8rem">Role
        <select name="role" style="${sel}">
          <option>reader</option>
          <option selected>editor</option>
          <option>admin</option>
        </select>
      </label>
      <button class="grant-btn" style="padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer">Grant</button>
      <span class="grant-err" style="color:#c00;font-size:0.8rem;display:none"></span>
    </div>
  `;
  wrap.querySelector('.grant-btn')!.addEventListener('click', async () => {
    const userId = (wrap.querySelector('[name="user"]') as HTMLSelectElement).value;
    const role = (wrap.querySelector('[name="role"]') as HTMLSelectElement).value;
    const errEl = wrap.querySelector('.grant-err') as HTMLElement;
    try {
      errEl.style.display = 'none';
      await onGrant(userId, role);
    } catch (e: unknown) {
      errEl.textContent = e instanceof Error ? e.message : String(e);
      errEl.style.display = 'inline';
    }
  });
  return wrap;
}

type EditData = { name: string; remote_url: string; default_branch: string; username: string; password: string };

function repoEditForm(repo: Repo, onSave: (d: EditData) => Promise<void>, onCancel: () => void): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:20px;margin-bottom:16px';
  const inp = 'width:100%;padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;margin-top:4px';
  wrap.innerHTML = `
    <h3 style="margin:0 0 16px;font-size:1rem">Edit repo</h3>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
      <label>Name<br><input name="name" value="${esc(repo.name)}" style="${inp}"></label>
      <label>Remote URL<br><input name="remote_url" value="${esc(repo.remote_url)}" style="${inp}"></label>
      <label>Default branch<br><input name="default_branch" value="${esc(repo.default_branch)}" style="${inp}"></label>
      <div></div>
      <label>Git username<br><input name="username" style="${inp}" placeholder="leave blank to keep existing"></label>
      <label>Password / token<br><input name="password" type="password" style="${inp}" placeholder="leave blank to keep existing"></label>
    </div>
    <div style="margin-top:16px;display:flex;gap:8px">
      <button class="save" style="padding:8px 20px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer">Save</button>
      <button class="cancel" style="padding:8px 20px;background:#e2e8f0;border:none;border-radius:6px;cursor:pointer">Cancel</button>
      <span class="err" style="color:#c00;align-self:center;display:none"></span>
    </div>
  `;
  wrap.querySelector('.cancel')!.addEventListener('click', onCancel);
  wrap.querySelector('.save')!.addEventListener('click', async () => {
    const v = (n: string) => (wrap.querySelector(`[name="${n}"]`) as HTMLInputElement).value.trim();
    const errEl = wrap.querySelector('.err') as HTMLElement;
    try {
      errEl.style.display = 'none';
      await onSave({ name: v('name'), remote_url: v('remote_url'), default_branch: v('default_branch'), username: v('username'), password: v('password') });
    } catch (e: unknown) {
      errEl.textContent = e instanceof Error ? e.message : String(e);
      errEl.style.display = 'inline';
    }
  });
  return wrap;
}

function mkBtn(text: string, variant: 'ghost' | 'danger' | 'danger-sm'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  const styles: Record<string, string> = {
    ghost: 'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer',
    danger: 'padding:8px 16px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer',
    'danger-sm': 'padding:4px 10px;background:none;border:none;color:#dc2626;cursor:pointer;text-decoration:underline;font-size:0.8rem',
  };
  b.style.cssText = styles[variant];
  return b;
}

function credentialStatusLabel(status: string | undefined): string {
  switch (status) {
    case 'verified': return '✓ verified (read)';
    case 'auth_failed': return '✗ auth failed';
    case 'unverified': return '• unverified';
    default: return '—';
  }
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
