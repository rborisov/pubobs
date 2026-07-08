import { checkUpdate, startUpdate, getUpdateStatus, type UpdateStatus } from '../api';
import { loadingRow } from '../spinner';

// How often to poll status while an update is running. The backend's own
// `docker compose up -d --build` step briefly kills and restarts the very
// container serving this request, so a handful of polls will fail with a
// network error mid-restart — the loop treats that as transient ("reconnecting")
// rather than a hard failure, and keeps polling until the new container answers.
const POLL_INTERVAL_MS = 2000;

export async function updatesView(): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:760px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  const title = document.createElement('h2');
  title.style.cssText = 'margin:0 0 8px;font-size:1.25rem';
  title.textContent = 'Updates';
  wrap.appendChild(title);

  const hint = document.createElement('p');
  hint.style.cssText = 'margin:0 0 24px;color:#64748b;font-size:0.875rem';
  hint.textContent = 'Check for and apply new PubObs server releases directly from here — no SSH required. Applying an update briefly restarts the service.';
  wrap.appendChild(hint);

  const panelWrap = document.createElement('div');
  wrap.appendChild(panelWrap);
  panelWrap.appendChild(loadingRow('Checking for updates…'));

  let polling = false;
  let lastStatus: UpdateStatus | null = null;

  async function refresh(): Promise<void> {
    try {
      const status = await checkUpdate();
      render(status);
    } catch (e: unknown) {
      panelWrap.replaceChildren(errBox(e));
    }
  }

  async function pollUntilSettled(): Promise<void> {
    if (polling) return;
    polling = true;
    try {
      for (;;) {
        await sleep(POLL_INTERVAL_MS);
        let status: UpdateStatus;
        try {
          status = await getUpdateStatus();
        } catch {
          // The service is very likely mid-restart (docker compose rebuilding
          // and recreating this very container) — keep polling instead of
          // surfacing a scary error for what's an expected, brief blip.
          render(lastStatus, 'Waiting for the service to come back after restart…');
          continue;
        }
        render(status);
        if (status.status !== 'running') return;
      }
    } finally {
      polling = false;
    }
  }

  function render(status: UpdateStatus | null, overrideMessage?: string): void {
    if (status) lastStatus = status;
    panelWrap.innerHTML = '';
    panelWrap.appendChild(buildPanel(status, overrideMessage, {
      onCheck: async () => {
        panelWrap.replaceChildren(loadingRow('Checking for updates…'));
        await refresh();
      },
      onApply: async () => {
        try {
          const started = await startUpdate();
          render(started);
          void pollUntilSettled();
        } catch (e: unknown) {
          panelWrap.appendChild(errBox(e));
        }
      },
    }));
  }

  await refresh();
  return wrap;
}

interface PanelActions {
  onCheck: () => void | Promise<void>;
  onApply: () => void | Promise<void>;
}

function buildPanel(status: UpdateStatus | null, overrideMessage: string | undefined, actions: PanelActions): HTMLElement {
  const panel = document.createElement('section');
  panel.style.cssText = 'background:#f8fafc;border:1px solid #dbe3ef;border-radius:10px;padding:20px 22px';

  const row = document.createElement('div');
  row.style.cssText = 'display:flex;align-items:flex-start;gap:16px;justify-content:space-between;flex-wrap:wrap';

  const meta = document.createElement('div');
  meta.style.flex = '1';
  meta.style.minWidth = '240px';

  const summary = document.createElement('div');
  summary.style.cssText = 'font-size:0.9rem;color:#0f172a;line-height:1.7';
  summary.innerHTML = status
    ? `Installed: <code style="background:#e2e8f0;padding:1px 6px;border-radius:4px">${esc(status.current_short || 'unknown')}</code>` +
      `&nbsp;&nbsp;Latest: <code style="background:#e2e8f0;padding:1px 6px;border-radius:4px">${esc(status.latest_short || 'unknown')}</code>`
    : '';
  meta.appendChild(summary);

  const message = document.createElement('div');
  message.style.cssText = `margin-top:8px;font-size:0.85rem;color:${statusColor(status)};line-height:1.6`;
  message.textContent = overrideMessage ?? statusMessage(status);
  meta.appendChild(message);

  row.appendChild(meta);

  const actionsRow = document.createElement('div');
  actionsRow.style.cssText = 'display:flex;gap:8px;flex-wrap:wrap';

  const running = status?.status === 'running';

  const checkBtn = mkBtn('Check for updates', 'ghost');
  checkBtn.disabled = running;
  checkBtn.addEventListener('click', () => void actions.onCheck());
  actionsRow.appendChild(checkBtn);

  const applyBtn = mkBtn(running ? 'Updating…' : 'Apply update', 'primary');
  applyBtn.disabled = running;
  applyBtn.addEventListener('click', async () => {
    if (!status?.update_available) {
      if (!confirm('No newer version was detected. Apply the update anyway?')) return;
    }
    applyBtn.disabled = true;
    checkBtn.disabled = true;
    await actions.onApply();
  });
  actionsRow.appendChild(applyBtn);

  row.appendChild(actionsRow);
  panel.appendChild(row);

  if (running) {
    const progress = document.createElement('div');
    progress.style.marginTop = '14px';
    progress.appendChild(loadingRow('Applying update — this restarts the service, so a brief connection blip is expected.'));
    panel.appendChild(progress);
  }

  if (status?.log) {
    const log = document.createElement('pre');
    log.style.cssText = 'margin:14px 0 0;background:#0f172a;color:#dbeafe;border-radius:8px;padding:12px;overflow:auto;max-height:280px;font-size:0.72rem;line-height:1.55;white-space:pre-wrap';
    log.textContent = status.log;
    panel.appendChild(log);
  }

  return panel;
}

function statusMessage(status: UpdateStatus | null): string {
  if (!status) return '';
  if (status.message) return status.message;
  if (status.update_available) return 'A newer server version is available.';
  if (status.status === 'idle') return 'Up to date.';
  return 'No update is currently required.';
}

function statusColor(status: UpdateStatus | null): string {
  if (!status) return '#475569';
  if (status.status === 'error') return '#b91c1c';
  if (status.status === 'running') return '#92400e';
  if (status.update_available) return '#92400e';
  return '#166534';
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function mkBtn(text: string, variant: 'primary' | 'ghost'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  b.style.cssText = variant === 'primary'
    ? 'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem'
    : 'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer;font-size:0.875rem';
  b.addEventListener('mouseenter', () => { if (!b.disabled) b.style.opacity = '0.9'; });
  b.addEventListener('mouseleave', () => { b.style.opacity = '1'; });
  return b;
}

function errBox(e: unknown): HTMLElement {
  const box = document.createElement('div');
  box.style.cssText = 'background:#fff7ed;border:1px solid #fdba74;border-radius:10px;padding:16px;color:#9a3412;font-size:0.875rem';
  box.textContent = e instanceof Error ? e.message : String(e);
  return box;
}

function esc(s: string): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
