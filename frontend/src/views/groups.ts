import {
  listGroups, createGroup, deleteGroup,
  listGroupMembers, addGroupMember, removeGroupMember, setGroupMemberRole,
  type Group, type GroupMember, type Me,
} from '../api';

export async function groupsView(me: Me): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let groups: Group[];
  try {
    groups = await listGroups();
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;margin-bottom:24px';
  const title = document.createElement('h2');
  title.style.cssText = 'margin:0;font-size:1.25rem;flex:1';
  title.textContent = 'Groups';
  header.appendChild(title);

  const newBtn = mkBtn('+ New group', 'primary');
  header.appendChild(newBtn);
  wrap.appendChild(header);

  const formWrap = document.createElement('div');
  wrap.appendChild(formWrap);

  newBtn.addEventListener('click', () => {
    if (formWrap.firstChild) { formWrap.innerHTML = ''; return; }
    formWrap.appendChild(groupForm(async name => {
      await createGroup(name);
      groups = await listGroups();
      renderGroups(listWrap, groups, me);
      formWrap.innerHTML = '';
    }, () => { formWrap.innerHTML = ''; }));
  });

  const listWrap = document.createElement('div');
  wrap.appendChild(listWrap);
  renderGroups(listWrap, groups, me);

  return wrap;
}

function renderGroups(container: HTMLElement, groups: Group[], me: Me): void {
  container.innerHTML = '';

  if (groups.length === 0) {
    const p = document.createElement('p');
    p.style.color = '#888';
    p.textContent = 'No groups yet.';
    container.appendChild(p);
    return;
  }

  for (const group of groups) {
    container.appendChild(groupCard(group, me, () => renderGroups(container, groups.filter(g => g.id !== group.id), me)));
  }
}

function groupCard(group: Group, me: Me, onDeleted: () => void): HTMLElement {
  const card = document.createElement('div');
  card.style.cssText = 'border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:16px';

  const cardHeader = document.createElement('div');
  cardHeader.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:12px';
  const nameEl = document.createElement('span');
  nameEl.style.cssText = 'font-weight:500;font-size:1rem;flex:1';
  nameEl.textContent = group.name;
  cardHeader.appendChild(nameEl);

  const delBtn = mkBtn('Delete', 'link-danger');
  delBtn.addEventListener('click', async () => {
    if (!confirm(`Delete group "${group.name}"?`)) return;
    try {
      await deleteGroup(group.id);
      onDeleted();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
    }
  });
  cardHeader.appendChild(delBtn);
  card.appendChild(cardHeader);

  const membersWrap = document.createElement('div');
  card.appendChild(membersWrap);

  loadMembers(membersWrap, group.id, me);

  return card;
}

async function loadMembers(container: HTMLElement, groupId: string, me: Me): Promise<void> {
  let members: GroupMember[];
  try {
    members = await listGroupMembers(groupId);
  } catch (e: unknown) {
    container.innerHTML = `<p style="color:#c00;font-size:0.8rem">${e instanceof Error ? e.message : String(e)}</p>`;
    return;
  }
  renderMembers(container, groupId, members, me);
}

function renderMembers(container: HTMLElement, groupId: string, members: GroupMember[], me: Me): void {
  container.innerHTML = '';

  if (members.length === 0) {
    const p = document.createElement('p');
    p.style.cssText = 'color:#94a3b8;font-size:0.85rem;margin:0 0 8px';
    p.textContent = 'No members.';
    container.appendChild(p);
  } else {
    const list = document.createElement('div');
    list.style.cssText = 'display:flex;flex-direction:column;gap:6px;margin-bottom:12px';

    for (const m of members) {
      const row = document.createElement('div');
      row.style.cssText = 'display:flex;align-items:center;gap:8px;font-size:0.85rem';

      const uid = document.createElement('span');
      uid.style.cssText = 'flex:1;color:#374151;font-family:monospace';
      uid.textContent = m.user_id;

      const roleToggle = mkBtn(m.role === 'admin' ? 'Admin' : 'Member', m.role === 'admin' ? 'toggle-on' : 'toggle-off');
      roleToggle.addEventListener('click', async () => {
        const next = m.role === 'admin' ? 'member' : 'admin';
        roleToggle.disabled = true;
        try {
          await setGroupMemberRole(groupId, m.user_id, next);
          m.role = next;
          roleToggle.textContent = next === 'admin' ? 'Admin' : 'Member';
          applyToggleStyle(roleToggle, next === 'admin');
        } catch (e: unknown) {
          alert(e instanceof Error ? e.message : String(e));
        } finally {
          roleToggle.disabled = false;
        }
      });

      const removeBtn = mkBtn('Remove', 'link-danger');
      removeBtn.addEventListener('click', async () => {
        removeBtn.disabled = true;
        try {
          await removeGroupMember(groupId, m.user_id);
          await loadMembers(container, groupId, me);
        } catch (e: unknown) {
          alert(e instanceof Error ? e.message : String(e));
          removeBtn.disabled = false;
        }
      });

      row.appendChild(uid);
      row.appendChild(roleToggle);
      row.appendChild(removeBtn);
      list.appendChild(row);
    }
    container.appendChild(list);
  }

  // Add member form
  const addRow = document.createElement('div');
  addRow.style.cssText = 'display:flex;gap:8px;align-items:center';
  const input = document.createElement('input');
  input.placeholder = 'User ID';
  input.style.cssText = 'padding:4px 8px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.8rem;width:220px';
  const addBtn = mkBtn('Add', 'ghost');
  const errSpan = document.createElement('span');
  errSpan.style.cssText = 'color:#c00;font-size:0.75rem;display:none';
  addRow.appendChild(input);
  addRow.appendChild(addBtn);
  addRow.appendChild(errSpan);
  container.appendChild(addRow);

  addBtn.addEventListener('click', async () => {
    const uid = input.value.trim();
    if (!uid) return;
    errSpan.style.display = 'none';
    addBtn.disabled = true;
    try {
      await addGroupMember(groupId, uid);
      input.value = '';
      await loadMembers(container, groupId, me);
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'inline';
      addBtn.disabled = false;
    }
  });
}

function groupForm(onSave: (name: string) => Promise<void>, onCancel: () => void): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:20px;margin-bottom:16px';

  const label = document.createElement('label');
  label.style.cssText = 'font-family:system-ui,sans-serif;font-size:0.8rem;font-weight:500;color:#374151';
  label.textContent = 'Group name';
  const input = document.createElement('input');
  input.placeholder = 'e.g. Engineering';
  input.style.cssText = 'display:block;width:100%;margin-top:4px;padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem';
  label.appendChild(input);
  wrap.appendChild(label);

  const footer = document.createElement('div');
  footer.style.cssText = 'margin-top:12px;display:flex;gap:8px;align-items:center';
  const saveBtn = mkBtn('Create', 'primary');
  const cancelBtn = mkBtn('Cancel', 'ghost');
  const errSpan = document.createElement('span');
  errSpan.style.cssText = 'color:#c00;font-size:0.8rem;display:none';
  footer.appendChild(saveBtn);
  footer.appendChild(cancelBtn);
  footer.appendChild(errSpan);
  wrap.appendChild(footer);

  cancelBtn.addEventListener('click', onCancel);
  saveBtn.addEventListener('click', async () => {
    const name = input.value.trim();
    if (!name) return;
    errSpan.style.display = 'none';
    saveBtn.disabled = true;
    saveBtn.textContent = 'Creating…';
    try {
      await onSave(name);
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'inline';
      saveBtn.disabled = false;
      saveBtn.textContent = 'Create';
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
    primary: 'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem',
    ghost: 'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer;font-size:0.875rem',
    link: 'background:none;border:none;cursor:pointer;color:#5B6B8E;text-decoration:underline;font-size:0.8rem;padding:0',
    'link-danger': 'background:none;border:none;cursor:pointer;color:#dc2626;text-decoration:underline;font-size:0.8rem;padding:0',
  };
  b.style.cssText = styles[variant];
  return b;
}
