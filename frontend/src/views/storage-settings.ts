import {
  listStorageDestinations, createStorageDestination, updateStorageDestination, deleteStorageDestination,
  getStorageUsage,
  type StorageDestination, type StorageDestinationInput, type StorageUsage,
} from '../api';

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = n / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(1)} ${units[i]}`;
}

export async function storageSettingsView(): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  const title = document.createElement('h2');
  title.style.cssText = 'margin:0 0 8px;font-size:1.25rem';
  title.textContent = 'Storage';
  wrap.appendChild(title);

  const hint = document.createElement('p');
  hint.style.cssText = 'margin:0 0 24px;color:#64748b;font-size:0.875rem';
  hint.textContent = 'Render blobs and media assets are stored locally by default. Add S3-compatible destinations and assign individual repos to them from each repo’s detail page.';
  wrap.appendChild(hint);

  const usageWrap = document.createElement('div');
  usageWrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:24px;font-size:0.875rem';
  wrap.appendChild(usageWrap);

  const destTitle = document.createElement('h3');
  destTitle.style.cssText = 'font-size:1rem;margin:0 0 12px';
  destTitle.textContent = 'Destinations';
  wrap.appendChild(destTitle);

  const listWrap = document.createElement('div');
  wrap.appendChild(listWrap);

  const formWrap = document.createElement('div');
  wrap.appendChild(formWrap);

  const addBtn = document.createElement('button');
  addBtn.textContent = 'Add destination';
  addBtn.style.cssText = 'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem;margin-top:8px';
  addBtn.addEventListener('click', () => {
    formWrap.innerHTML = '';
    formWrap.appendChild(buildDestinationForm(null, async (input) => {
      await createStorageDestination(input);
      formWrap.innerHTML = '';
      await reloadDestinations();
      await reloadUsage();
    }, () => { formWrap.innerHTML = ''; }));
  });
  wrap.appendChild(addBtn);

  async function reloadUsage(): Promise<void> {
    usageWrap.innerHTML = `<div style="color:#64748b;margin:0">Loading usage… <span style="opacity:0.7">(scanning local storage — this can take a moment for large repos)</span></div>`;
    let usage: StorageUsage;
    try {
      usage = await getStorageUsage();
    } catch (e: unknown) {
      usageWrap.innerHTML = `<p style="color:#c00;margin:0">${e instanceof Error ? e.message : String(e)}</p>`;
      return;
    }
    const localTotal = usage.local.db_bytes + usage.local.repos_bytes + usage.local.renders_bytes + usage.local.assets_bytes;
    const destLines = usage.destinations.map((d) => {
      if (d.error) {
        return `<div><strong>${esc(d.name)}:</strong> <span style="color:#c00">unavailable — ${esc(d.error)}</span></div>`;
      }
      return `<div><strong>${esc(d.name)}:</strong> ${formatBytes(d.renders_bytes)} renders, ${formatBytes(d.assets_bytes)} assets</div>`;
    }).join('');
    const row = (label: string, value: string): string =>
      `<div style="display:flex;justify-content:space-between;max-width:320px"><span>${label}</span><span>${value}</span></div>`;
    usageWrap.innerHTML = `
      <div style="font-weight:600;margin-bottom:6px">Local storage</div>
      ${row('Database', formatBytes(usage.local.db_bytes))}
      ${row('Repos', formatBytes(usage.local.repos_bytes))}
      ${row('Renders', formatBytes(usage.local.renders_bytes))}
      ${row('Assets', formatBytes(usage.local.assets_bytes))}
      <div style="border-top:1px solid #cbd5e1;margin:4px 0;max-width:320px"></div>
      ${row('<strong>Total local</strong>', `<strong>${formatBytes(localTotal)}</strong>`)}
      ${row('Free disk', formatBytes(usage.local.free_bytes))}
      ${destLines ? `<div style="font-weight:600;margin:10px 0 4px">Destination usage</div>${destLines}` : ''}
    `;
  }

  async function reloadDestinations(): Promise<void> {
    let dests: StorageDestination[];
    try {
      dests = await listStorageDestinations();
    } catch (e: unknown) {
      listWrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
      return;
    }
    renderDestinations(listWrap, dests);
  }

  function renderDestinations(container: HTMLElement, dests: StorageDestination[]): void {
    container.innerHTML = '';

    if (dests.length === 0) {
      const p = document.createElement('p');
      p.style.cssText = 'color:#64748b;font-style:italic;margin:0 0 16px';
      p.textContent = 'No destinations — all repos store locally.';
      container.appendChild(p);
      return;
    }

    const table = document.createElement('table');
    table.style.marginBottom = '16px';
    table.innerHTML = `<thead><tr><th>Name</th><th>Bucket</th><th></th></tr></thead>`;
    const tbody = document.createElement('tbody');

    for (const dest of dests) {
      const row = document.createElement('tr');
      const nameCell = document.createElement('td');
      nameCell.textContent = dest.name;
      const bucketCell = document.createElement('td');
      bucketCell.style.fontFamily = 'monospace';
      bucketCell.textContent = dest.s3_bucket;

      const actionCell = document.createElement('td');
      const editBtn = document.createElement('button');
      editBtn.textContent = 'Edit';
      editBtn.style.cssText = 'background:none;border:none;cursor:pointer;color:#5B6B8E;text-decoration:underline;font-size:0.8rem;padding:0;margin-right:12px';
      editBtn.addEventListener('click', () => {
        formWrap.innerHTML = '';
        formWrap.appendChild(buildDestinationForm(dest, async (input) => {
          await updateStorageDestination(dest.id, input);
          formWrap.innerHTML = '';
          await reloadDestinations();
          await reloadUsage();
        }, () => { formWrap.innerHTML = ''; }));
      });

      const delBtn = document.createElement('button');
      delBtn.textContent = 'Delete';
      delBtn.style.cssText = 'background:none;border:none;cursor:pointer;color:#dc2626;text-decoration:underline;font-size:0.8rem;padding:0';
      const delErr = document.createElement('span');
      delErr.style.cssText = 'color:#c00;font-size:0.8rem;display:none;margin-left:8px';
      delBtn.addEventListener('click', async () => {
        if (!confirm(`Delete destination "${dest.name}"?`)) return;
        delErr.style.display = 'none';
        delBtn.disabled = true;
        try {
          await deleteStorageDestination(dest.id);
          await reloadDestinations();
          await reloadUsage();
        } catch (e: unknown) {
          delErr.textContent = e instanceof Error ? e.message : String(e);
          delErr.style.display = 'inline';
          delBtn.disabled = false;
        }
      });

      actionCell.appendChild(editBtn);
      actionCell.appendChild(delBtn);
      actionCell.appendChild(delErr);

      row.appendChild(nameCell);
      row.appendChild(bucketCell);
      row.appendChild(actionCell);
      tbody.appendChild(row);
    }

    table.appendChild(tbody);
    container.appendChild(table);
  }

  await reloadUsage();
  await reloadDestinations();
  return wrap;
}

function buildDestinationForm(
  dest: StorageDestination | null,
  onSave: (input: StorageDestinationInput) => Promise<void>,
  onCancel: () => void,
): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-top:8px';

  const heading = document.createElement('h4');
  heading.style.cssText = 'margin:0 0 12px;font-size:0.875rem;font-weight:600';
  heading.textContent = dest ? 'Edit destination' : 'Add destination';
  wrap.appendChild(heading);

  const fields = document.createElement('div');
  fields.style.cssText = 'display:flex;flex-direction:column;gap:10px;margin-bottom:16px';

  const makeField = (labelText: string, value: string, placeholder = ''): HTMLInputElement => {
    const label = document.createElement('label');
    label.style.cssText = 'display:block;font-size:0.8rem;font-weight:500';
    label.textContent = labelText;
    const input = document.createElement('input');
    input.type = 'text';
    input.value = value;
    input.placeholder = placeholder;
    input.style.cssText = 'display:block;width:100%;margin-top:4px;padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem;box-sizing:border-box';
    label.appendChild(input);
    fields.appendChild(label);
    return input;
  };

  const nameInput = makeField('Name', dest?.name ?? '');
  const endpointInput = makeField('Endpoint', dest?.s3_endpoint ?? '', 's3.amazonaws.com');
  const bucketInput = makeField('Bucket', dest?.s3_bucket ?? '');
  const accessKeyInput = makeField('Access key', dest?.s3_access_key ?? '');
  const secretKeyInput = makeField('Secret key', '', dest ? 'leave blank to keep existing' : '');
  secretKeyInput.type = 'password';
  const regionInput = makeField('Region', dest?.s3_region ?? '', 'us-east-1');

  const sslLabel = document.createElement('label');
  sslLabel.style.cssText = 'display:flex;align-items:center;gap:6px;font-size:0.8rem';
  const sslCheckbox = document.createElement('input');
  sslCheckbox.type = 'checkbox';
  sslCheckbox.checked = dest?.s3_use_ssl ?? true;
  sslLabel.appendChild(sslCheckbox);
  sslLabel.appendChild(document.createTextNode('Use SSL'));
  fields.appendChild(sslLabel);

  wrap.appendChild(fields);

  const errSpan = document.createElement('p');
  errSpan.style.cssText = 'color:#c00;font-size:0.8rem;display:none;margin:0 0 12px';
  wrap.appendChild(errSpan);

  const btnRow = document.createElement('div');
  btnRow.style.cssText = 'display:flex;gap:8px';

  const saveBtn = document.createElement('button');
  saveBtn.textContent = 'Save';
  saveBtn.style.cssText = 'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem';
  saveBtn.addEventListener('click', async () => {
    errSpan.style.display = 'none';
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    try {
      await onSave({
        name: nameInput.value.trim(),
        s3_endpoint: endpointInput.value.trim(),
        s3_bucket: bucketInput.value.trim(),
        s3_access_key: accessKeyInput.value.trim(),
        s3_secret_key: secretKeyInput.value,
        s3_region: regionInput.value.trim(),
        s3_use_ssl: sslCheckbox.checked,
      });
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'block';
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  const cancelBtn = document.createElement('button');
  cancelBtn.textContent = 'Cancel';
  cancelBtn.style.cssText = 'padding:8px 16px;background:#e2e8f0;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem';
  cancelBtn.addEventListener('click', onCancel);

  btnRow.appendChild(saveBtn);
  btnRow.appendChild(cancelBtn);
  wrap.appendChild(btnRow);

  return wrap;
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
