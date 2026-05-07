import { type Me } from '../api';

export async function groupsView(_me: Me): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';
  const title = document.createElement('h2');
  title.style.cssText = 'margin:0 0 24px;font-size:1.25rem';
  title.textContent = 'Groups';
  wrap.appendChild(title);
  return wrap;
}
