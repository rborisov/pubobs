const STYLE_ID = 'pubobs-reader-styles';

export function ensureReaderStyles(): void {
  if (document.getElementById(STYLE_ID)) return;
  const style = document.createElement('style');
  style.id = STYLE_ID;
  style.textContent = `
    :root {
      --r-bg: #ffffff;
      --r-text: #0f172a;
      --r-text-muted: #64748b;
      --r-text-faint: #94a3b8;
      --r-border: #e2e8f0;
      --r-hover-bg: #f1f5f9;
      --r-code-bg: #f1f5f9;
      --r-tag-bg: #f1f5f9;
      --r-tag-text: #475569;
      --r-link: #2563eb;
      --r-card-bg: #f8fafc;
      --r-error: #cc0000;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --r-bg: #0f172a;
        --r-text: #e2e8f0;
        --r-text-muted: #94a3b8;
        --r-text-faint: #64748b;
        --r-border: #1e293b;
        --r-hover-bg: #1e293b;
        --r-code-bg: #1e293b;
        --r-tag-bg: #1e293b;
        --r-tag-text: #94a3b8;
        --r-link: #60a5fa;
        --r-card-bg: #1e293b;
        --r-error: #f87171;
      }
    }
    body { background: var(--r-bg); color: var(--r-text); }
    .r-muted { color: var(--r-text-muted); }
    .r-faint { color: var(--r-text-faint); }
    .r-border-bottom { border-bottom: 1px solid var(--r-border); }
    .r-note-link {
      display: flex; align-items: baseline; justify-content: space-between;
      padding: 12px 16px; border-radius: 6px; text-decoration: none;
      color: var(--r-text); transition: background 0.1s;
    }
    .r-note-link:hover { background: var(--r-hover-bg); }
    .r-note-link-title { font-weight: 500; }
    .r-note-link-date { font-size: 0.75rem; color: var(--r-text-faint); white-space: nowrap; margin-left: 16px; }
    .r-section-heading {
      font-size: 0.875rem; font-weight: 600; color: var(--r-text-muted);
      margin: 0 0 12px; text-transform: uppercase; letter-spacing: 0.05em;
    }
    .r-backlink-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
    .r-backlink-list a { color: var(--r-text); font-size: 0.875rem; }
    .r-tag {
      font-size: 0.7rem; padding: 2px 8px; background: var(--r-tag-bg);
      border-radius: 999px; color: var(--r-tag-text);
    }
    .r-comment-card {
      padding: 12px 16px; background: var(--r-card-bg);
      border: 1px solid var(--r-border); border-radius: 6px;
    }
    .r-comment-author { font-size: 0.8rem; font-weight: 600; color: var(--r-text); }
    .r-comment-date { font-size: 0.75rem; color: var(--r-text-faint); }
    .r-comment-body { font-size: 0.875rem; color: var(--r-text); }
    .r-form-input {
      width: 100%; padding: 10px; border: 1px solid var(--r-border); border-radius: 6px;
      font-size: 0.875rem; resize: vertical; min-height: 80px; font-family: inherit;
      box-sizing: border-box; background: var(--r-bg); color: var(--r-text);
    }
    .r-btn-primary {
      padding: 8px 16px; background: var(--r-text); color: var(--r-bg);
      border: none; border-radius: 6px; cursor: pointer; font-size: 0.875rem;
    }
    .r-error { color: var(--r-error); }
    .r-link { color: var(--r-link); }
  `;
  document.head.appendChild(style);
}
