import {
  getStorageSettings, updateStorageSettings, getStorageUsage, triggerStorageMigration,
  type StorageSettings, type StorageUsage,
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
  hint.textContent = 'Render blobs and media assets can be stored locally or on S3-compatible object storage. Changes apply immediately.';
  wrap.appendChild(hint);

  const usageWrap = document.createElement('div');
  usageWrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:24px;font-size:0.875rem';
  wrap.appendChild(usageWrap);

  const formWrap = document.createElement('div');
  wrap.appendChild(formWrap);

  async function reloadUsage(): Promise<void> {
    let usage: StorageUsage;
    try {
      usage = await getStorageUsage();
    } catch (e: unknown) {
      usageWrap.innerHTML = `<p style="color:#c00;margin:0">${e instanceof Error ? e.message : String(e)}</p>`;
      return;
    }
    const localTotal = usage.local_repos_bytes + usage.local_renders_bytes + usage.local_assets_bytes;
    const s3Total = usage.s3_renders_bytes + usage.s3_assets_bytes;
    usageWrap.innerHTML = `
      <strong>Local:</strong> ${formatBytes(localTotal)}
      (repos: ${formatBytes(usage.local_repos_bytes)},
       renders: ${formatBytes(usage.local_renders_bytes)},
       assets: ${formatBytes(usage.local_assets_bytes)})
      &nbsp;·&nbsp;
      <strong>S3:</strong> ${formatBytes(s3Total)}
      &nbsp;·&nbsp;
      <strong>Free disk:</strong> ${formatBytes(usage.local_free_bytes)}
    `;
  }

  async function renderForm(): Promise<void> {
    let settings: StorageSettings;
    try {
      settings = await getStorageSettings();
    } catch (e: unknown) {
      formWrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
      return;
    }
    formWrap.innerHTML = '';
    formWrap.appendChild(buildForm(settings, async () => {
      await reloadUsage();
      await renderForm();
    }));
  }

  await reloadUsage();
  await renderForm();
  return wrap;
}

function buildForm(settings: StorageSettings, onSaved: () => Promise<void>): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'background:#fff;border:1px solid #e2e8f0;border-radius:8px;padding:16px';

  const typeLabel = document.createElement('label');
  typeLabel.style.cssText = 'display:block;font-size:0.8rem;font-weight:500;margin-bottom:4px';
  typeLabel.textContent = 'Backend';
  const typeSelect = document.createElement('select');
  typeSelect.style.cssText = 'padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem;margin-bottom:16px';
  for (const opt of ['local', 's3'] as const) {
    const o = document.createElement('option');
    o.value = opt;
    o.textContent = opt === 's3' ? 'S3-compatible' : 'Local disk';
    o.selected = settings.store_type === opt;
    typeSelect.appendChild(o);
  }
  typeLabel.appendChild(typeSelect);
  wrap.appendChild(typeLabel);

  const s3Fields = document.createElement('div');
  s3Fields.style.cssText = 'display:flex;flex-direction:column;gap:10px;margin-bottom:16px';

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
    s3Fields.appendChild(label);
    return input;
  };

  const endpointInput = makeField('Endpoint', settings.s3_endpoint, 's3.amazonaws.com');
  const bucketInput = makeField('Bucket', settings.s3_bucket);
  const accessKeyInput = makeField('Access key', settings.s3_access_key);
  const secretKeyInput = makeField('Secret key', '', 'leave blank to keep existing');
  secretKeyInput.type = 'password';
  const regionInput = makeField('Region', settings.s3_region, 'us-east-1');

  const sslLabel = document.createElement('label');
  sslLabel.style.cssText = 'display:flex;align-items:center;gap:6px;font-size:0.8rem';
  const sslCheckbox = document.createElement('input');
  sslCheckbox.type = 'checkbox';
  sslCheckbox.checked = settings.s3_use_ssl;
  sslLabel.appendChild(sslCheckbox);
  sslLabel.appendChild(document.createTextNode('Use SSL'));
  s3Fields.appendChild(sslLabel);

  wrap.appendChild(s3Fields);

  const updateVisibility = (): void => {
    s3Fields.style.display = typeSelect.value === 's3' ? 'flex' : 'none';
  };
  updateVisibility();
  typeSelect.addEventListener('change', updateVisibility);

  const errSpan = document.createElement('p');
  errSpan.style.cssText = 'color:#c00;font-size:0.8rem;display:none;margin:0 0 12px';
  wrap.appendChild(errSpan);

  const saveBtn = document.createElement('button');
  saveBtn.textContent = 'Save';
  saveBtn.style.cssText = 'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem;margin-right:8px';
  saveBtn.addEventListener('click', async () => {
    errSpan.style.display = 'none';
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    try {
      await updateStorageSettings({
        store_type: typeSelect.value as 'local' | 's3',
        s3_endpoint: endpointInput.value.trim(),
        s3_bucket: bucketInput.value.trim(),
        s3_access_key: accessKeyInput.value.trim(),
        s3_secret_key: secretKeyInput.value,
        s3_region: regionInput.value.trim(),
        s3_use_ssl: sslCheckbox.checked,
      });
      // Applies live (Swap() on the backend, Task 7) — no restart, no
      // downtime, so we can reload the form/usage immediately.
      await onSaved();
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'block';
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });
  wrap.appendChild(saveBtn);

  if (settings.store_type === 's3') {
    const migrateBtn = document.createElement('button');
    migrateBtn.textContent = settings.migration_status === 'running'
      ? `Migrating… (${settings.migration_done}/${settings.migration_total})`
      : 'Migrate existing local data to S3';
    migrateBtn.disabled = settings.migration_status === 'running';
    migrateBtn.style.cssText = 'padding:8px 16px;background:#4BB585;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem';
    migrateBtn.addEventListener('click', async () => {
      migrateBtn.disabled = true;
      migrateBtn.textContent = 'Starting…';
      try {
        await triggerStorageMigration();
        await onSaved();
      } catch (e: unknown) {
        errSpan.textContent = e instanceof Error ? e.message : String(e);
        errSpan.style.display = 'block';
        migrateBtn.disabled = false;
        migrateBtn.textContent = 'Migrate existing local data to S3';
      }
    });
    wrap.appendChild(migrateBtn);
  }

  return wrap;
}
