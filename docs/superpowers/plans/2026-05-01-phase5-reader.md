# Phase 5 — PubObs Reader

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A public-facing reader so anyone can browse and read published notes. No sign-in required.

**Key design decisions:**
- Markdown → HTML is rendered **server-side** using `goldmark` during sync (not client-side). The `html_content` column in `note_snapshots` is already there; the backend was just storing `""` because the plugin sends empty. Fix: always render from `md_content` in `handleSync`, ignore the plugin-provided value.
- New **unauthenticated** routes under `/pub/…` serve the rendered HTML. The existing `/api/repos/{id}/notes` routes remain auth-gated for other uses.
- Reader lives in the **existing SPA** under `#/read/{repoId}` and `#/read/{repoId}/{notePath}`. No second HTML page needed.
- The hash router is extended to support a **trailing `*` wildcard** so note paths with slashes (`posts/hello.md`) work as route params.

---

## Parts overview (execute one per session)

| Part | Tasks | What it produces |
|------|-------|-----------------|
| **A** | 0–1 | Backend: goldmark rendering in sync + public read endpoints |
| **B** | 2–4 | Frontend: wildcard router, public API client, reader views |

---

## Part A — Backend

### Task 0: Render markdown to HTML in `handleSync`

Add `goldmark` (the standard Go markdown library) and render `md_content` to HTML during sync, overriding the empty `html_content` from the plugin.

- [ ] **Step 1: Add goldmark dependency**

```bash
cd backend && go get github.com/yuin/goldmark@latest
go get github.com/yuin/goldmark-meta@latest
```

- [ ] **Step 2: Add render helper** — new file `backend/internal/api/render.go`:

```go
package api

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,        // tables, strikethrough, task lists, autolinks
		extension.Footnote,
	),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		html.WithXHTML(),
	),
)

func renderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "<pre>" + src + "</pre>"
	}
	return buf.String()
}
```

- [ ] **Step 3: Use `renderMarkdown` in `handleSync`** — in `backend/internal/api/sync.go`, replace the line that calls `UpsertSnapshot` to render HTML from `f.MDContent`:

Find:
```go
deps.Store.UpsertSnapshot(r.Context(), note.ID, f.HTMLContent, string(metaJSON), claims.UserID, sha)
```

Replace with:
```go
deps.Store.UpsertSnapshot(r.Context(), note.ID, renderMarkdown(f.MDContent), string(metaJSON), claims.UserID, sha)
```

- [ ] **Step 4: Verify** — `cd backend && go build ./...` — no errors.

---

### Task 1: Public read endpoints

New file `backend/internal/api/pub.go` with two unauthenticated handlers, plus new routes in `router.go`.

**Public note list** — `GET /pub/{repoId}`:
Returns repo name + list of notes with title derived from first heading in `metadata_json`.

**Public note view** — `GET /pub/{repoId}/notes/{notePath...}`:
Returns rendered HTML, metadata, tags, backlinks.

**File:** `backend/internal/api/pub.go`

```go
package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

func handlePubListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		notes, err := deps.Store.ListNotes(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}

		type noteItem struct {
			ID        string `json:"id"`
			Path      string `json:"path"`
			Title     string `json:"title"`
			SyncedAt  string `json:"synced_at"`
		}
		type repoInfo struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}

		items := make([]noteItem, 0, len(notes))
		for _, n := range notes {
			snap, _ := deps.Store.GetSnapshot(r.Context(), n.ID)
			title := noteTitle(n.Path, snap)
			syncedAt := ""
			if snap != nil {
				syncedAt = snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			items = append(items, noteItem{ID: n.ID, Path: n.Path, Title: title, SyncedAt: syncedAt})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"repo":  repoInfo{ID: repo.ID, Name: repo.Name},
			"notes": items,
		})
	}
}

func handlePubGetNote(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")

		note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
		if err != nil || note == nil {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		snap, err := deps.Store.GetSnapshot(r.Context(), note.ID)
		if err != nil || snap == nil {
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}

		var meta struct {
			Headings    []string       `json:"headings"`
			Tags        []string       `json:"tags"`
			Frontmatter map[string]any `json:"frontmatter"`
		}
		_ = json.Unmarshal([]byte(snap.MetadataJSON), &meta)

		backlinks, _ := deps.Store.GetBacklinks(r.Context(), repoID, notePath)
		type backlinkItem struct {
			Path  string `json:"path"`
			Title string `json:"title"`
		}
		bl := make([]backlinkItem, 0, len(backlinks))
		for _, b := range backlinks {
			bsnap, _ := deps.Store.GetSnapshot(r.Context(), b.ID)
			bl = append(bl, backlinkItem{Path: b.Path, Title: noteTitle(b.Path, bsnap)})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":             note.ID,
			"path":           note.Path,
			"title":          noteTitle(notePath, snap),
			"html_content":   snap.HTMLContent,
			"tags":           meta.Tags,
			"frontmatter":    meta.Frontmatter,
			"git_commit_sha": snap.GitCommitSHA,
			"synced_at":      snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"backlinks":      bl,
		})
	}
}

// noteTitle extracts a display title from snapshot metadata or falls back to the filename.
func noteTitle(path string, snap interface{ GetMeta() string }) string {
	type snapWithMeta interface {
		GetMeta() string
	}
	// snap is *model.NoteSnapshot or nil; use type assertion on the concrete type
	return noteTitleFromPath(path)
}

func noteTitleFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
```

Wait — the `noteTitle` helper needs to actually read `metadata_json` from the snapshot. Let me write it more simply without a confusing interface:

```go
// noteTitle returns the first heading from snapshot metadata, falling back to the filename stem.
func noteTitle(path string, snap *model.NoteSnapshot) string {
	if snap != nil && snap.MetadataJSON != "" {
		var meta struct {
			Headings []string `json:"headings"`
		}
		if err := json.Unmarshal([]byte(snap.MetadataJSON), &meta); err == nil && len(meta.Headings) > 0 {
			return meta.Headings[0]
		}
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
```

Use this version in the file. The full `pub.go` using this corrected helper:

```go
package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/model"
)

func handlePubListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		notes, err := deps.Store.ListNotes(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}

		type noteItem struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			Title    string `json:"title"`
			SyncedAt string `json:"synced_at"`
		}

		items := make([]noteItem, 0, len(notes))
		for _, n := range notes {
			snap, _ := deps.Store.GetSnapshot(r.Context(), n.ID)
			syncedAt := ""
			if snap != nil {
				syncedAt = snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			items = append(items, noteItem{
				ID: n.ID, Path: n.Path,
				Title:    noteTitle(n.Path, snap),
				SyncedAt: syncedAt,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"repo":  map[string]string{"id": repo.ID, "name": repo.Name},
			"notes": items,
		})
	}
}

func handlePubGetNote(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")

		note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
		if err != nil || note == nil {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		snap, err := deps.Store.GetSnapshot(r.Context(), note.ID)
		if err != nil || snap == nil {
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}

		var meta struct {
			Tags        []string       `json:"tags"`
			Frontmatter map[string]any `json:"frontmatter"`
		}
		_ = json.Unmarshal([]byte(snap.MetadataJSON), &meta)

		backlinks, _ := deps.Store.GetBacklinks(r.Context(), repoID, notePath)
		type backlinkItem struct {
			Path  string `json:"path"`
			Title string `json:"title"`
		}
		bl := make([]backlinkItem, 0, len(backlinks))
		for _, b := range backlinks {
			bsnap, _ := deps.Store.GetSnapshot(r.Context(), b.ID)
			bl = append(bl, backlinkItem{Path: b.Path, Title: noteTitle(b.Path, bsnap)})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":             note.ID,
			"path":           note.Path,
			"title":          noteTitle(notePath, snap),
			"html_content":   snap.HTMLContent,
			"tags":           meta.Tags,
			"frontmatter":    meta.Frontmatter,
			"git_commit_sha": snap.GitCommitSHA,
			"synced_at":      snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"backlinks":      bl,
		})
	}
}

func noteTitle(path string, snap *model.NoteSnapshot) string {
	if snap != nil && snap.MetadataJSON != "" {
		var meta struct {
			Headings []string `json:"headings"`
		}
		if err := json.Unmarshal([]byte(snap.MetadataJSON), &meta); err == nil && len(meta.Headings) > 0 {
			return meta.Headings[0]
		}
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
```

**File:** `backend/internal/api/router.go` — add public routes **before** the authenticated group and before the catch-all file server:

```go
// Public reader (no auth)
r.Get("/pub/{repoId}", handlePubListNotes(deps))
r.Get("/pub/{repoId}/notes/*", handlePubGetNote(deps))
```

- [ ] **Step 1:** Write `backend/internal/api/pub.go` with the full content above (corrected version with `noteTitle` using `*model.NoteSnapshot`).
- [ ] **Step 2:** Add the two public routes to `backend/internal/api/router.go`.
- [ ] **Step 3:** Verify — `cd backend && go build ./...` — no errors.
- [ ] **Step 4:** Smoke-test — sync a note from Obsidian, then `curl http://localhost:8080/pub/{repoId}` — confirm JSON response with title populated.

---

## Part B — Frontend

### Task 2: Extend router + add public API functions

**Router wildcard support** — note paths contain slashes (`posts/hello.md`), so `#/read/{repoId}/posts/hello.md` needs a wildcard segment. Update `frontend/src/router.ts`:

Replace the `register` function body with:

```typescript
export function register(path: string, factory: ViewFactory): void {
  const keys: string[] = [];
  let src = path;
  let wildcard = false;

  if (src.endsWith('/*')) {
    src = src.slice(0, -2);
    wildcard = true;
  }

  src = src.replace(/:([^/]+)/g, (_: string, k: string) => {
    keys.push(k);
    return '([^/]+)';
  });

  const pattern = wildcard
    ? new RegExp(`^${src}(?:/(.*))?$`)
    : new RegExp(`^${src}$`);

  if (wildcard) keys.push('*');
  routes.push({ pattern, keys, factory });
}
```

**Public API functions** — add to `frontend/src/api.ts`:

```typescript
export interface PubNote {
  id: string;
  path: string;
  title: string;
  synced_at: string;
}

export interface PubRepo {
  id: string;
  name: string;
}

export interface PubNoteDetail {
  id: string;
  path: string;
  title: string;
  html_content: string;
  tags: string[];
  frontmatter: Record<string, unknown>;
  git_commit_sha: string;
  synced_at: string;
  backlinks: Array<{ path: string; title: string }>;
}

export async function pubListNotes(repoId: string): Promise<{ repo: PubRepo; notes: PubNote[] }> {
  const resp = await fetch(`/pub/${repoId}`);
  return json(resp);
}

export async function pubGetNote(repoId: string, notePath: string): Promise<PubNoteDetail> {
  const resp = await fetch(`/pub/${repoId}/notes/${notePath}`);
  return json(resp);
}
```

- [ ] **Step 1:** Update `register()` in `frontend/src/router.ts` with wildcard support.
- [ ] **Step 2:** Append the public API types and functions to `frontend/src/api.ts`.
- [ ] **Step 3:** Verify — `cd frontend && npx tsc --noEmit` — no errors.

---

### Task 3: Note list reader view

**File:** `frontend/src/views/reader-list.ts`

```typescript
import { pubListNotes, type PubNote, type PubRepo } from '../api';

export async function readerListView(repoId: string): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:720px;margin:0 auto;padding:40px 24px;font-family:system-ui,sans-serif';

  let data: { repo: PubRepo; notes: PubNote[] };
  try {
    data = await pubListNotes(repoId);
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const { repo, notes } = data;

  const header = document.createElement('header');
  header.style.cssText = 'margin-bottom:40px;border-bottom:1px solid #e2e8f0;padding-bottom:24px';
  header.innerHTML = `<h1 style="margin:0 0 4px;font-size:1.75rem;font-weight:700">${esc(repo.name)}</h1>
    <p style="margin:0;color:#64748b;font-size:0.875rem">${notes.length} note${notes.length !== 1 ? 's' : ''}</p>`;
  wrap.appendChild(header);

  if (notes.length === 0) {
    const empty = document.createElement('p');
    empty.style.color = '#94a3b8';
    empty.textContent = 'No notes published yet.';
    wrap.appendChild(empty);
    return wrap;
  }

  const list = document.createElement('ul');
  list.style.cssText = 'list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:2px';

  for (const note of notes) {
    const li = document.createElement('li');
    const a = document.createElement('a');
    a.href = `#/read/${repoId}/${note.path}`;
    a.style.cssText =
      'display:flex;align-items:baseline;justify-content:space-between;padding:12px 16px;' +
      'border-radius:6px;text-decoration:none;color:#0f172a;transition:background 0.1s';
    a.addEventListener('mouseenter', () => { a.style.background = '#f1f5f9'; });
    a.addEventListener('mouseleave', () => { a.style.background = ''; });

    const titleSpan = document.createElement('span');
    titleSpan.style.fontWeight = '500';
    titleSpan.textContent = note.title;

    const dateSpan = document.createElement('span');
    dateSpan.style.cssText = 'font-size:0.75rem;color:#94a3b8;white-space:nowrap;margin-left:16px';
    dateSpan.textContent = note.synced_at ? new Date(note.synced_at).toLocaleDateString() : '';

    a.appendChild(titleSpan);
    a.appendChild(dateSpan);
    li.appendChild(a);
    list.appendChild(li);
  }

  wrap.appendChild(list);
  return wrap;
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
```

- [ ] **Step 1:** Write `frontend/src/views/reader-list.ts`.

---

### Task 4: Note detail reader view + wire everything up

**File:** `frontend/src/views/reader-note.ts`

```typescript
import { pubGetNote, type PubNoteDetail } from '../api';

export async function readerNoteView(repoId: string, notePath: string): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:720px;margin:0 auto;padding:40px 24px;font-family:system-ui,sans-serif';

  let note: PubNoteDetail;
  try {
    note = await pubGetNote(repoId, notePath);
  } catch (e: unknown) {
    wrap.innerHTML = `
      <a href="#/read/${repoId}" style="color:#64748b;font-size:0.875rem;text-decoration:none">← Back</a>
      <p style="color:#c00;margin-top:16px">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  // Back link
  const back = document.createElement('a');
  back.href = `#/read/${repoId}`;
  back.style.cssText = 'color:#64748b;font-size:0.875rem;text-decoration:none;display:block;margin-bottom:32px';
  back.textContent = '← Back';
  wrap.appendChild(back);

  // Article
  const article = document.createElement('article');

  // Title
  const h1 = document.createElement('h1');
  h1.style.cssText = 'margin:0 0 8px;font-size:2rem;font-weight:700;line-height:1.2';
  h1.textContent = note.title;
  article.appendChild(h1);

  // Meta row
  const meta = document.createElement('div');
  meta.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:32px;flex-wrap:wrap';

  const date = document.createElement('span');
  date.style.cssText = 'font-size:0.8rem;color:#94a3b8';
  date.textContent = note.synced_at ? new Date(note.synced_at).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' }) : '';
  meta.appendChild(date);

  for (const tag of note.tags ?? []) {
    const badge = document.createElement('span');
    badge.style.cssText =
      'font-size:0.7rem;padding:2px 8px;background:#f1f5f9;border-radius:999px;color:#475569';
    badge.textContent = tag;
    meta.appendChild(badge);
  }
  article.appendChild(meta);

  // Content
  const content = document.createElement('div');
  content.className = 'prose';
  content.style.cssText =
    'line-height:1.7;color:#1a1a1a;' +
    'font-size:1rem;';
  addProseStyles(content);
  content.innerHTML = note.html_content;
  article.appendChild(content);

  wrap.appendChild(article);

  // Backlinks
  if (note.backlinks?.length > 0) {
    const section = document.createElement('section');
    section.style.cssText =
      'margin-top:48px;padding-top:24px;border-top:1px solid #e2e8f0';
    const blTitle = document.createElement('h2');
    blTitle.style.cssText = 'font-size:0.875rem;font-weight:600;color:#64748b;margin:0 0 12px;text-transform:uppercase;letter-spacing:0.05em';
    blTitle.textContent = 'Linked from';
    section.appendChild(blTitle);

    const blList = document.createElement('ul');
    blList.style.cssText = 'list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:4px';
    for (const bl of note.backlinks) {
      const li = document.createElement('li');
      const a = document.createElement('a');
      a.href = `#/read/${repoId}/${bl.path}`;
      a.style.cssText = 'color:#0f172a;font-size:0.875rem';
      a.textContent = bl.title;
      li.appendChild(a);
      blList.appendChild(li);
    }
    section.appendChild(blList);
    wrap.appendChild(section);
  }

  return wrap;
}

// Inject minimal prose styles so rendered markdown looks good.
function addProseStyles(el: HTMLElement): void {
  const style = document.createElement('style');
  style.textContent = `
    .prose h1,.prose h2,.prose h3,.prose h4 { margin:1.5em 0 0.5em; line-height:1.3; }
    .prose h1 { font-size:1.75rem; } .prose h2 { font-size:1.375rem; } .prose h3 { font-size:1.125rem; }
    .prose p { margin:0 0 1em; }
    .prose ul,.prose ol { margin:0 0 1em 1.5em; }
    .prose li { margin-bottom:0.25em; }
    .prose a { color:#2563eb; }
    .prose code { background:#f1f5f9; padding:1px 5px; border-radius:3px; font-size:0.875em; }
    .prose pre { background:#1e293b; color:#e2e8f0; padding:16px; border-radius:6px; overflow:auto; margin:0 0 1em; }
    .prose pre code { background:none; padding:0; font-size:0.875rem; }
    .prose blockquote { border-left:3px solid #cbd5e1; margin:0 0 1em; padding:0 0 0 1em; color:#64748b; }
    .prose table { border-collapse:collapse; width:100%; margin:0 0 1em; }
    .prose th,.prose td { border:1px solid #e2e8f0; padding:6px 12px; text-align:left; }
    .prose th { background:#f8fafc; font-weight:600; }
    .prose img { max-width:100%; border-radius:4px; }
    .prose hr { border:none; border-top:1px solid #e2e8f0; margin:2em 0; }
  `;
  el.appendChild(style);
}
```

**Update `frontend/src/main.ts`** — register reader routes and skip auth redirect for them:

Add imports at the top:
```typescript
import { readerListView } from './views/reader-list';
import { readerNoteView } from './views/reader-note';
```

Add route registrations after the existing ones:
```typescript
register('/read/:repoId', ({ repoId }) => readerListView(repoId));
register('/read/:repoId/*', params => readerNoteView(params['repoId'], params['*'] ?? ''));
```

Update the `isAuthenticated()` guard in `boot()` to let reader routes through:
```typescript
if (!isAuthenticated() && !location.hash.startsWith('#/read')) {
  navigate('/login');
  const content = document.createElement('div');
  content.id = 'content';
  app.appendChild(content);
  start(content);
  return;
}
```

For reader routes, also skip the nav bar (unauthenticated readers don't need it):
```typescript
if (isAuthenticated() && !location.hash.startsWith('#/read')) {
  renderNav(app);
}
```

- [ ] **Step 1:** Write `frontend/src/views/reader-list.ts`.
- [ ] **Step 2:** Write `frontend/src/views/reader-note.ts`.
- [ ] **Step 3:** Update `frontend/src/main.ts` — add imports, register reader routes, update auth guard, update nav guard.
- [ ] **Step 4:** Build — `cd frontend && npm run build` — no TypeScript errors.
- [ ] **Step 5:** Backend build — `cd backend && go build ./...` — no errors.
- [ ] **Step 6:** End-to-end test:
  - Start backend
  - Sync notes from Obsidian
  - Visit `http://localhost:8080/#/read/{repoId}` — note list appears
  - Click a note — rendered HTML appears
  - Confirm heading-based titles, tags, date

---

## Appendix — New API endpoints

| Method | Path | Auth | Response |
|--------|------|------|----------|
| `GET` | `/pub/{repoId}` | none | `{repo: {id,name}, notes: [{id,path,title,synced_at}]}` |
| `GET` | `/pub/{repoId}/notes/{path...}` | none | `{id,path,title,html_content,tags,frontmatter,git_commit_sha,synced_at,backlinks}` |
