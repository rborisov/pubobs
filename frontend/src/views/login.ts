import { beginAuth } from '../auth';

export function loginView(): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText =
    'max-width:400px;margin:120px auto;padding:0 24px;font-family:system-ui,sans-serif;text-align:center';
  wrap.innerHTML = `
    <h1 style="font-size:1.5rem;font-weight:600;margin-bottom:8px">PubObs</h1>
    <p style="color:#555;margin-bottom:24px">Sign in to manage repos and access.</p>
    <button id="signin-btn"
      style="padding:10px 24px;background:#0f172a;color:#fff;border:none;border-radius:6px;font-size:1rem;cursor:pointer">
      Sign in
    </button>
    <p id="err-msg" style="color:#c00;margin-top:16px;display:none"></p>
  `;
  wrap.querySelector('#signin-btn')!.addEventListener('click', async () => {
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
