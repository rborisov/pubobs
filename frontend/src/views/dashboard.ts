import { listRepos, type Me, type Repo } from '../api';

export async function dashboardView(me: Me): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:860px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let repos: Repo[];
  try {
    repos = await listRepos();
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const greeting = document.createElement('div');
  greeting.style.cssText = 'margin-bottom:28px';
  greeting.innerHTML = `
    <h2 style="margin:0 0 4px;font-size:1.25rem">Hi, ${esc(me.name || me.email)}</h2>
    <p style="margin:0;color:#64748b;font-size:0.875rem">${esc(me.email)}</p>
  `;
  wrap.appendChild(greeting);

  if (repos.length === 0) {
    const empty = document.createElement('div');
    empty.style.cssText =
      'background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:32px;text-align:center;color:#64748b';
    empty.innerHTML = `
      <p style="margin:0 0 8px;font-size:1rem">No repos yet</p>
      <p style="margin:0;font-size:0.875rem">Ask your admin to grant you access to a repo.</p>
    `;
    wrap.appendChild(empty);
    return wrap;
  }

  for (const repo of repos) {
    wrap.appendChild(repoCard(repo, me));
  }

  return wrap;
}

function repoCard(repo: Repo, _me: Me): HTMLElement {
  const card = document.createElement('div');
  card.style.cssText =
    'background:#fff;border:1px solid #e2e8f0;border-radius:8px;padding:20px 24px;margin-bottom:16px';

  const isEditor = repo.role === 'editor' || repo.role === 'admin';
  const roleBadge = roleBadgeEl(repo.role);

  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:12px';
  const title = document.createElement('h3');
  title.style.cssText = 'margin:0;font-size:1rem;font-weight:600;flex:1';
  title.textContent = repo.name;
  header.appendChild(title);
  header.appendChild(roleBadge);
  card.appendChild(header);

  const actions = document.createElement('div');
  actions.style.cssText = 'display:flex;gap:8px;flex-wrap:wrap';

  const readBtn = mkBtn('Read notes', 'primary');
  readBtn.addEventListener('click', () => { location.hash = `#/read/${repo.id}`; });
  actions.appendChild(readBtn);

  card.appendChild(actions);

  if (isEditor) {
    card.appendChild(syncSection(repo));
  }

  return card;
}

function syncSection(repo: Repo): HTMLElement {
  const sec = document.createElement('details');
  sec.style.cssText = 'margin-top:16px;border-top:1px solid #e2e8f0;padding-top:16px';

  const summary = document.createElement('summary');
  summary.style.cssText =
    'cursor:pointer;font-size:0.875rem;font-weight:500;color:#0f172a;user-select:none;list-style:none';
  summary.innerHTML = `<span style="margin-right:6px">▸</span>Obsidian sync setup`;
  sec.addEventListener('toggle', () => {
    const arrow = summary.querySelector('span')!;
    arrow.textContent = sec.open ? '▾ ' : '▸ ';
  });

  const body = document.createElement('div');
  body.style.cssText = 'margin-top:12px;font-size:0.875rem;color:#475569;line-height:1.6';

  const backendUrl = `${location.protocol}//${location.host}`;

  body.innerHTML = `
    <ol style="margin:0 0 12px;padding-left:20px">
      <li>Install the <strong>PubObs</strong> community plugin in Obsidian.</li>
      <li>In plugin settings, set <strong>Backend URL</strong> to:</li>
    </ol>
  `;

  const urlRow = document.createElement('div');
  urlRow.style.cssText = 'display:flex;align-items:center;gap:8px;margin:0 0 12px 20px';

  const urlCode = document.createElement('code');
  urlCode.style.cssText =
    'background:#f1f5f9;padding:4px 10px;border-radius:4px;font-size:0.8rem;flex:1;overflow:auto';
  urlCode.textContent = backendUrl;

  const copyBtn = document.createElement('button');
  copyBtn.textContent = 'Copy';
  copyBtn.style.cssText =
    'padding:4px 10px;background:#0f172a;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:0.75rem;white-space:nowrap';
  copyBtn.addEventListener('click', () => {
    navigator.clipboard.writeText(backendUrl).then(() => {
      copyBtn.textContent = 'Copied!';
      setTimeout(() => { copyBtn.textContent = 'Copy'; }, 1500);
    });
  });

  urlRow.appendChild(urlCode);
  urlRow.appendChild(copyBtn);
  body.appendChild(urlRow);

  const rest = document.createElement('ol');
  rest.setAttribute('start', '3');
  rest.style.cssText = 'margin:0;padding-left:20px';
  rest.innerHTML = `
    <li>Click <strong>Sign in</strong> in the plugin and authenticate.</li>
    <li>In plugin settings, map <strong>${esc(repo.name)}</strong> to a vault folder.</li>
    <li>Use the <strong>Sync</strong> command to push notes, or <strong>Pull</strong> to fetch them.</li>
  `;
  body.appendChild(rest);

  sec.appendChild(summary);
  sec.appendChild(body);
  return sec;
}

function roleBadgeEl(role: string): HTMLElement {
  const span = document.createElement('span');
  span.textContent = role;
  const colors: Record<string, string> = {
    admin:        'background:#fef3c7;color:#92400e',
    editor:       'background:#dbeafe;color:#1e40af',
    commentator:  'background:#f3e8ff;color:#6b21a8',
    reader:       'background:#f0fdf4;color:#166534',
  };
  span.style.cssText =
    `font-size:0.7rem;font-weight:600;padding:2px 8px;border-radius:99px;text-transform:uppercase;` +
    (colors[role] ?? 'background:#f1f5f9;color:#475569');
  return span;
}

function mkBtn(text: string, variant: 'primary' | 'ghost'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  b.style.cssText = variant === 'primary'
    ? 'padding:7px 16px;background:#0f172a;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem'
    : 'padding:7px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer;font-size:0.875rem';
  return b;
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
