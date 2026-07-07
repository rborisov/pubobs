// Small CSS spinner shared by the router (full-page route transitions) and
// individual views (inline "Loading…" replacements).

let stylesInjected = false;

function ensureSpinnerStyles(): void {
  if (stylesInjected) return;
  stylesInjected = true;
  if (document.getElementById('pubobs-spinner-styles')) return;

  const style = document.createElement('style');
  style.id = 'pubobs-spinner-styles';
  style.textContent = `
    @keyframes pubobs-spin { to { transform: rotate(360deg); } }
    .pubobs-spinner {
      display: inline-block;
      box-sizing: border-box;
      border-radius: 50%;
      border-style: solid;
      border-color: currentColor;
      border-top-color: transparent;
      opacity: 0.65;
      animation: pubobs-spin 0.7s linear infinite;
      flex-shrink: 0;
    }
  `;
  document.head.appendChild(style);
}

/** A bare spinning ring. Color follows the current text color (`currentColor`). */
export function createSpinner(size = 18): HTMLElement {
  ensureSpinnerStyles();
  const el = document.createElement('span');
  el.className = 'pubobs-spinner';
  el.style.width = `${size}px`;
  el.style.height = `${size}px`;
  el.style.borderWidth = `${Math.max(2, Math.round(size / 8))}px`;
  el.setAttribute('aria-hidden', 'true');
  return el;
}

/** Spinner + label, inline — drop-in replacement for a "Loading…" text node. */
export function loadingRow(text = 'Loading…', size = 16): HTMLElement {
  const row = document.createElement('div');
  row.style.cssText = 'display:flex;align-items:center;gap:8px;color:inherit';
  row.appendChild(createSpinner(size));
  const label = document.createElement('span');
  label.textContent = text;
  row.appendChild(label);
  return row;
}

/** Centered spinner filling the space where a whole page/section is about to appear. */
export function fullPageLoader(text = 'Loading…'): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;padding:96px 24px;color:#94a3b8;font-family:system-ui,sans-serif';
  wrap.appendChild(createSpinner(28));
  const label = document.createElement('span');
  label.style.fontSize = '0.875rem';
  label.textContent = text;
  wrap.appendChild(label);
  return wrap;
}
