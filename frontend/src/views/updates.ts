import { checkUpdate, startUpdate, getUpdateStatus, type UpdateStatus } from '../api';
import { loadingRow } from '../spinner';

// How often to poll status while an update is running. The backend's own
// `docker compose up -d --build` step briefly kills and restarts the very
// container serving this request, so a handful of polls will fail with a
// network error mid-restart — the loop treats that as transient ("reconnecting")
// rather than a hard failure, and keeps polling until the new container answers.
const POLL_INTERVAL_MS = 2000;

// How long to keep treating "the service isn't answering" as an expected
// restart blip before deciding the update likely failed to come back up. A
// container swap is a few seconds; a new image that crash-loops on boot never
// answers again — and since the updater rode inside the container that just
// died, nothing is left to report that failure for us. Past this window we
// stop waiting and tell the admin to check the server / fall back to
// install.sh, rather than spinning "reconnecting…" forever.
const MAX_RESTART_WAIT_MS = 180_000;

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

  // targetVersion is the SHA this run is applying (the `latest` reported when
  // the update started); it's what we confirm the service actually booted on
  // once it comes back, rather than assuming the swap took.
  async function pollUntilSettled(targetVersion: string): Promise<void> {
    if (polling) return;
    polling = true;
    let downSince: number | null = null;
    try {
      for (;;) {
        await sleep(POLL_INTERVAL_MS);
        let status: UpdateStatus;
        try {
          status = await getUpdateStatus();
        } catch {
          // Service isn't answering. During the build phase the old container
          // stays up and keeps replying "running", so this only happens once
          // the container is actually being recreated. That's normally a
          // seconds-long blip — but a new image that won't boot never comes
          // back, so we bound the wait instead of spinning forever.
          const now = Date.now();
          if (downSince === null) downSince = now;
          if (now - downSince > MAX_RESTART_WAIT_MS) {
            render(lastStatus,
              'The service has not come back after the restart. The update may have failed to start — check the server, or run "install.sh --update" from a shell.',
              'error');
            return;
          }
          render(lastStatus, 'Waiting for the service to come back after restart…');
          continue;
        }
        downSince = null;
        if (status.status === 'running') {
          render(status);
          continue;
        }
        // A failure caught before the container swap (bad clone/build) is
        // reported by the still-alive old container as status "error" — show
        // it, with its log, as-is.
        if (status.status === 'error') {
          render(status);
          return;
        }
        // Otherwise the service answered again after the restart. Don't take
        // "it's up" as proof the update applied — confirm the installed
        // version really advanced to the one we were rolling out.
        await confirmOutcome(targetVersion);
        return;
      }
    } finally {
      polling = false;
    }
  }

  // confirmOutcome runs once the service is reachable again post-restart. It
  // re-checks the deployed version against the remote so we can tell a real
  // success (installed SHA advanced to the target) apart from a restart that
  // came back still running the old image (swap silently didn't take).
  async function confirmOutcome(targetVersion: string): Promise<void> {
    let status: UpdateStatus;
    try {
      status = await checkUpdate();
    } catch {
      render(lastStatus,
        'Update applied, but the follow-up version check did not respond — verify the installed version manually.',
        'warn');
      return;
    }
    if (targetVersion && !sameVersion(status.current, targetVersion)) {
      render(status,
        `The service came back but still reports ${status.current_short || 'an older version'} — the update may not have taken. Check the server logs, or run "install.sh --update".`,
        'error');
      return;
    }
    render(status, `Update complete — now running ${status.current_short || 'the latest version'}.`, 'ok');
  }

  function render(status: UpdateStatus | null, overrideMessage?: string, overrideTone?: Tone): void {
    if (status) lastStatus = status;
    panelWrap.innerHTML = '';
    panelWrap.appendChild(buildPanel(status, overrideMessage, overrideTone, {
      onCheck: async () => {
        panelWrap.replaceChildren(loadingRow('Checking for updates…'));
        await refresh();
      },
      onApply: async () => {
        try {
          const started = await startUpdate();
          render(started);
          void pollUntilSettled(started.latest || '');
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

function buildPanel(status: UpdateStatus | null, overrideMessage: string | undefined, overrideTone: Tone | undefined, actions: PanelActions): HTMLElement {
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
  message.style.cssText = `margin-top:8px;font-size:0.85rem;color:${overrideTone ? toneColor(overrideTone) : statusColor(status)};line-height:1.6`;
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

// Tone lets the poll/confirm logic force a message color for synthesized
// messages (a bounded-wait failure, or a post-restart success/warning) that
// don't map cleanly onto the raw status.status the server last reported.
type Tone = 'error' | 'warn' | 'ok';

function toneColor(tone: Tone): string {
  switch (tone) {
    case 'error': return '#b91c1c';
    case 'warn': return '#92400e';
    case 'ok': return '#166534';
  }
}

// sameVersion mirrors the backend's VersionsEqual (update.go): exact match on
// full SHAs, otherwise a shared prefix of at least 7 hex chars, so an
// abbreviated short SHA still compares equal to a full one.
function sameVersion(a: string, b: string): boolean {
  a = (a || '').trim().toLowerCase();
  b = (b || '').trim().toLowerCase();
  if (!a || !b) return a === b;
  if (a === b) return true;
  const min = Math.min(a.length, b.length);
  if (min < 7) return false;
  return a.slice(0, min) === b.slice(0, min);
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
