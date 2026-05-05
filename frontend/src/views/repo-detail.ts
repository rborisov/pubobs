import {
  listRepos, listRepoAccess, grantAccess, revokeAccess, listUsers,
  updateRepo, deleteRepo,
  type Repo, type RepoAccess, type User,
} from '../api';
import { navigate } from '../router';

export async function repoDetailView(id: string): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let repo: Repo | undefined;
  let accessList: RepoAccess[];
  let users: User[];

  try {
    const [repos, access, allUsers] = await Promise.all([
      listRepos(), listRepoAccess(id), listUsers(),
    ]);
    repo = repos.find(r => r.id === id);
    accessList = access;
    users = allUsers;
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  if (!repo) {
    wrap.innerHTML = `<p style="color:#c00">Repo not found.</p>`;
    return wrap;
  }

  render(wrap, repo, accessList, users);
  return wrap;
}

function render(wrap: HTMLElement, repo: Repo, accessList: RepoAccess[], users: User[]): void {
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
      <span style="font-weight:600">Status</span><span>${repo.is_cloned ? '● cloned' : '○ not cloned'}</span>
    </div>
  `;
  wrap.appendChild(card);

  const editWrap = document.createElement('div');
  wrap.appendChild(editWrap);

  editBtn.addEventListener('click', () => {
    if (editWrap.firstChild) { editWrap.innerHTML = ''; return; }
    editWrap.appendChild(repoEditForm(repo, async (data) => {
      await updateRepo(repo.id, data);
      const repos = await listRepos();
      const fresh = repos.find(r => r.id === repo.id);
      if (fresh) Object.assign(repo, fresh);
      editWrap.innerHTML = '';
      render(wrap, repo, accessList, users);
    }, () => { editWrap.innerHTML = ''; }));
  });

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
      table.innerHTML = `<thead><tr><th>Type</th><th>User</th><th>Role</th><th></th></tr></thead>`;
      const tbody = document.createElement('tbody');
      for (const entry of list) {
        const user = users.find(u => u.id === entry.principal_id);
        const row = document.createElement('tr');
        row.innerHTML = `
          <td>${esc(entry.principal_type)}</td>
          <td>${esc(user?.email ?? entry.principal_id)}</td>
          <td>${esc(entry.role)}</td>
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

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
