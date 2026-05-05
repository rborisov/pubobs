import { beginAuth } from '../auth';

const LOGO_MARK = `
<svg width="64" height="64" viewBox="0 0 44 44" fill="none" xmlns="http://www.w3.org/2000/svg">
  <path d="M4 36 L1 33 L1 9 L4 6 L36 6 L36 36 Z" fill="#8094AF" opacity="0.35"/>
  <path d="M6 39 L3 36 L3 12 L6 9 L39 9 L39 39 Z" fill="#6B7E9E" opacity="0.55"/>
  <path d="M9 42 L6 39 L6 15 L9 12 L42 12 L42 42 Z" fill="#5B6B8E"/>
  <line x1="17" y1="19" x2="26" y2="29" stroke="#2D3F56" stroke-width="2.2" stroke-linecap="round"/>
  <line x1="35" y1="19" x2="26" y2="29" stroke="#4BB585" stroke-width="2.2" stroke-linecap="round"/>
  <line x1="26" y1="29" x2="26" y2="38" stroke="#2D3F56" stroke-width="2.2" stroke-linecap="round"/>
  <circle cx="17" cy="19" r="3.8" fill="#4BB585"/>
  <circle cx="35" cy="19" r="3.8" fill="#4BB585"/>
  <circle cx="26" cy="29" r="3.8" fill="#2D3F56"/>
  <circle cx="26" cy="38" r="3.8" fill="#2D3F56"/>
</svg>`.trim();

export function loginView(): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText =
    'max-width:380px;margin:100px auto;padding:40px 32px;font-family:system-ui,sans-serif;text-align:center;' +
    'background:#fff;border-radius:12px;box-shadow:0 4px 24px rgba(45,63,86,0.10)';

  wrap.innerHTML = `
    <div style="display:flex;justify-content:center;margin-bottom:20px">${LOGO_MARK}</div>
    <h1 style="font-size:1.75rem;font-weight:700;margin:0 0 6px;color:#2D3F56;letter-spacing:-0.02em">PubObs</h1>
    <p style="color:#8094AF;margin:0 0 28px;font-size:0.9rem">Publish your Obsidian notes</p>
    <button id="signin-btn"
      style="width:100%;padding:11px 24px;background:#5B6B8E;color:#fff;border:none;border-radius:8px;
             font-size:0.95rem;font-weight:500;cursor:pointer;transition:background 0.15s;letter-spacing:0.01em">
      Sign in with OIDC
    </button>
    <p id="err-msg" style="color:#c00;margin-top:16px;display:none;font-size:0.875rem"></p>
  `;

  const btn = wrap.querySelector('#signin-btn') as HTMLButtonElement;
  btn.addEventListener('mouseenter', () => { btn.style.background = '#4a5a7a'; });
  btn.addEventListener('mouseleave', () => { btn.style.background = '#5B6B8E'; });

  btn.addEventListener('click', async () => {
    const errEl = wrap.querySelector('#err-msg') as HTMLElement;
    try {
      errEl.style.display = 'none';
      await beginAuth();
    } catch (e: unknown) {
      errEl.textContent = e instanceof Error ? e.message : String(e);
      errEl.style.display = 'block';
    }
  });

  return wrap;
}
