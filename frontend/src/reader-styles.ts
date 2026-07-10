const STYLE_ID = 'pubobs-reader-styles';

/**
 * Injects the reader's (dark-mode-aware) CSS once, and marks <body> so the
 * dark-mode `body.pubobs-reader-theme` rule below applies. The router clears
 * the body class on every navigation, so leaving the reader for an admin
 * page (which has no dark theme) always restores the normal light body.
 */
export function ensureReaderStyles(): void {
  document.body.classList.add('pubobs-reader-theme');
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
      --r-link: #5B6B8E;
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
        --r-link: #8094AF;
        --r-card-bg: #1e293b;
        --r-error: #f87171;
      }
    }
    /* Scoped to a body class (toggled by the router) rather than a bare
       "body" selector, so the reader's dark-mode theme never bleeds into
       the admin panel, which has no dark theme of its own. */
    body.pubobs-reader-theme { background: var(--r-bg); color: var(--r-text); }
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
    .r-note-link-filename {
      font-size: 0.75rem; color: var(--r-text-faint); font-family: ui-monospace, monospace;
    }
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
      padding: 8px 16px; background: #5B6B8E; color: #fff;
      border: none; border-radius: 6px; cursor: pointer; font-size: 0.875rem;
    }
    .r-btn-primary:hover { background: #4a5a7a; }
    .r-btn-ghost {
      background: transparent; border: 1px solid var(--r-border, #e2e8f0);
      border-radius: 5px; cursor: pointer; color: var(--r-muted, #64748b);
    }
    .r-btn-ghost:hover { background: var(--r-border, #e2e8f0); }
    .r-error { color: var(--r-error); }
    .r-link { color: var(--r-link); }
    .r-share-wrap { position: relative; display: inline-block; }
    .r-share-menu {
      position: absolute; top: calc(100% + 4px); right: 0; z-index: 20;
      background: var(--r-bg); border: 1px solid var(--r-border); border-radius: 8px;
      box-shadow: 0 4px 16px rgba(0,0,0,0.12); padding: 6px; min-width: 240px;
      display: flex; flex-direction: column; gap: 2px;
    }
    .r-share-menu-item {
      display: block; width: 100%; text-align: left; padding: 8px 10px; border-radius: 5px;
      border: none; background: transparent; color: var(--r-text); font-size: 0.8rem;
      cursor: pointer; font-family: inherit;
    }
    .r-share-menu-item:hover { background: var(--r-hover-bg); }
    .r-share-menu-item.r-share-revoke { color: var(--r-error); }
    .r-share-badge {
      font-size: 0.7rem; padding: 2px 8px; border-radius: 999px;
      background: var(--r-tag-bg); color: var(--r-tag-text); white-space: nowrap;
    }
    .r-note-row-actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; margin-left: 12px; }
    /* Make the selection highlight visible. Obsidian's CSS sets
       ::selection { background-color: var(--text-selection) }, but
       --text-selection resolves through --color-accent-hsl, which the theme
       only defines under .theme-light/.theme-dark — classes our reader page
       (body.pubobs-reader-theme) never sets. So the variable is invalid here
       and the highlight paints transparent: text selects (and copies) fine but
       you can't see it. Define a concrete, theme-aware highlight that doesn't
       depend on Obsidian's accent chain, specific enough to beat the theme's
       bare ::selection rule. ::selection and ::-moz-selection can't be grouped
       (an unknown pseudo invalidates the whole selector list), so keep them
       as separate rules. */
    body.pubobs-reader-theme ::selection { background: rgba(91, 107, 142, 0.35); }
    body.pubobs-reader-theme ::-moz-selection { background: rgba(91, 107, 142, 0.35); }
    @media (prefers-color-scheme: dark) {
      body.pubobs-reader-theme ::selection { background: rgba(128, 148, 175, 0.45); }
      body.pubobs-reader-theme ::-moz-selection { background: rgba(128, 148, 175, 0.45); }
    }
    /* Force rendered notes to be selectable/copyable. Obsidian themes (and
       the app CSS they mimic) frequently set user-select:none on the preview
       view; rewriting that arbitrary CSS is fragile (vendor prefixes, !important,
       selectors we don't match), so instead we win here with a selector more
       specific than any single-class theme rule — body carries
       .pubobs-reader-theme, so this is (0,2,x) vs the theme's (0,1,x). */
    body.pubobs-reader-theme .markdown-rendered,
    body.pubobs-reader-theme .markdown-rendered * {
      -webkit-user-select: text !important;
      user-select: text !important;
      -webkit-touch-callout: default !important;
    }
    .markdown-rendered table {
      border-collapse: collapse !important; width: 100%; margin: 1.5em 0;
      font-size: 0.9em;
    }
    .markdown-rendered th, .markdown-rendered td {
      border: 1px solid var(--r-border) !important; padding: 6px 12px !important;
      text-align: left;
    }
    .markdown-rendered thead th {
      background: var(--r-card-bg) !important; font-weight: 600;
    }
    .markdown-rendered tbody tr:nth-child(even) td {
      background: var(--r-hover-bg) !important;
    }
    /* Long words / URLs shouldn't force the page wider than the screen. */
    .markdown-rendered { overflow-wrap: anywhere; }
    /* Wide code blocks scroll within themselves rather than off-screen.
       Wide tables are wrapped in an overflow-x container at render time
       (see reader-note.ts) for the same reason. */
    .markdown-rendered pre { overflow-x: auto; max-width: 100%; }

    /* ── Reader note-list layout (responsive) ─────────────────────────────
       Moved off inline styles so a mobile breakpoint can restyle them. On a
       phone the fixed 220px sidebar squeezed the note list into a sliver, so
       below 640px the sidebar + list stack vertically and rows lay out top-to
       -bottom instead of squeezing title vs. meta side by side. */
    .r-list-wrap { max-width: 1100px; margin: 0 auto; padding: 32px 24px; font-family: system-ui, sans-serif; }
    .r-list-body { display: flex; gap: 24px; align-items: flex-start; }
    .r-list-sidebar {
      width: 220px; flex-shrink: 0; position: sticky; top: 24px;
      max-height: calc(100vh - 80px); overflow-y: auto;
      display: flex; flex-direction: column; gap: 20px;
    }
    .r-list-main { flex: 1; min-width: 0; }
    .r-note-meta { display: flex; align-items: center; gap: 10px; flex-shrink: 0; margin-left: 16px; }
    @media (max-width: 640px) {
      .r-list-wrap { padding: 20px 14px; }
      .r-list-body { flex-direction: column; gap: 16px; }
      .r-list-sidebar {
        width: auto; position: static; top: auto; max-height: none;
        overflow: visible; gap: 14px;
      }
      .r-note-link { flex-direction: column; align-items: stretch; gap: 6px; }
      .r-note-link-title, .r-note-link-filename { overflow-wrap: anywhere; }
      .r-note-meta { margin-left: 0; flex-wrap: wrap; }
      .r-note-link-date { margin-left: 0; }
      .r-note-wrap { padding: 24px 14px !important; }
      .markdown-rendered h1, .r-note-title { font-size: 1.5rem !important; }
    }
  `;
  document.head.appendChild(style);
}
