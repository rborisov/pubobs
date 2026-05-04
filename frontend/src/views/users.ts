import { listUsers, setUserAdmin, setUserBanned, type User } from '../api';

export async function usersView(): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let users: User[];
  try {
    users = await listUsers();
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const title = document.createElement('h2');
  title.style.cssText = 'margin:0 0 24px;font-size:1.25rem';
  title.textContent = 'Users';
  wrap.appendChild(title);

  const tableWrap = document.createElement('div');
  wrap.appendChild(tableWrap);
  renderTable(tableWrap, users, async () => {
    users = await listUsers();
    renderTable(tableWrap, users, async () => {/* handled recursively */});
  });

  return wrap;
}

function renderTable(container: HTMLElement, users: User[], refresh: () => Promise<void>): void {
  container.innerHTML = '';

  if (users.length === 0) {
    const p = document.createElement('p');
    p.style.color = '#888';
    p.textContent = 'No users yet.';
    container.appendChild(p);
    return;
  }

  const table = document.createElement('table');
  table.innerHTML = `<thead><tr>
    <th>Email</th><th>Name</th><th>Admin</th><th>Status</th><th></th>
  </tr></thead>`;
  const tbody = document.createElement('tbody');

  for (const user of users) {
    const row = document.createElement('tr');

    const emailCell = document.createElement('td');
    emailCell.textContent = user.email;

    const nameCell = document.createElement('td');
    nameCell.style.color = '#64748b';
    nameCell.textContent = user.name;

    const adminCell = document.createElement('td');
    adminCell.textContent = user.is_instance_admin ? '✓ admin' : '—';
    adminCell.style.color = user.is_instance_admin ? '#16a34a' : '#94a3b8';

    const statusCell = document.createElement('td');
    statusCell.textContent = user.is_banned ? 'banned' : 'active';
    statusCell.style.color = user.is_banned ? '#dc2626' : '#475569';

    const actionsCell = document.createElement('td');
    actionsCell.style.cssText = 'white-space:nowrap;display:flex;gap:8px';

    const adminBtn = mkBtn(user.is_instance_admin ? 'Remove admin' : 'Make admin', 'link');
    adminBtn.addEventListener('click', async () => {
      adminBtn.disabled = true;
      try {
        await setUserAdmin(user.id, !user.is_instance_admin);
        await refresh();
      } catch (e: unknown) {
        alert(e instanceof Error ? e.message : String(e));
        adminBtn.disabled = false;
      }
    });

    const banBtn = mkBtn(user.is_banned ? 'Unban' : 'Ban', user.is_banned ? 'link' : 'link-danger');
    banBtn.addEventListener('click', async () => {
      if (!user.is_banned && !confirm(`Ban ${user.email}? They will be locked out immediately.`)) return;
      banBtn.disabled = true;
      try {
        await setUserBanned(user.id, !user.is_banned);
        await refresh();
      } catch (e: unknown) {
        alert(e instanceof Error ? e.message : String(e));
        banBtn.disabled = false;
      }
    });

    actionsCell.appendChild(adminBtn);
    actionsCell.appendChild(banBtn);

    row.appendChild(emailCell);
    row.appendChild(nameCell);
    row.appendChild(adminCell);
    row.appendChild(statusCell);
    row.appendChild(actionsCell);
    tbody.appendChild(row);
  }

  table.appendChild(tbody);
  container.appendChild(table);
}

function mkBtn(text: string, variant: 'link' | 'link-danger'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  const styles: Record<string, string> = {
    link: 'background:none;border:none;cursor:pointer;color:#0f172a;text-decoration:underline;font-size:0.8rem;padding:0',
    'link-danger': 'background:none;border:none;cursor:pointer;color:#dc2626;text-decoration:underline;font-size:0.8rem;padding:0',
  };
  b.style.cssText = styles[variant];
  return b;
}
