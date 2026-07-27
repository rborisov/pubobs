/**
 * Data files are non-note repo files (CSV/JSON/YAML/Obsidian `.base`) synced
 * verbatim between the repo and the vault. They are never rendered, never
 * encrypted, and never become notes.
 */

/**
 * Parses the `dataFileExtensions` setting: a comma-separated list, tolerant of
 * leading dots, surrounding whitespace, mixed case and duplicates.
 *
 * `md` is dropped rather than rejected — notes already have their own sync
 * path, and a user who types it is expressing a reasonable-but-redundant
 * intent, not an error worth failing the whole sync over. The backend refuses
 * it outright, so it must never be sent.
 *
 * This is a free-text box the user may still be mid-typo in, so malformed
 * entries are silently dropped rather than surfaced as an error — the same
 * `^[a-z0-9]{1,10}$` shape the backend itself requires (it 400s the whole
 * request on any violation). Filtering client-side means a stray semicolon
 * or symbol costs the user that one extension instead of failing the entire
 * pull or sync. The asymmetry (server rejects, client drops) is deliberate.
 */
export function parseDataFileExtensions(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(',')) {
    const ext = part.trim().replace(/^\./, '').toLowerCase();
    if (!ext || ext === 'md' || seen.has(ext) || !/^[a-z0-9]{1,10}$/.test(ext)) continue;
    seen.add(ext);
    out.push(ext);
  }
  return out;
}

/** True when path's final extension is in exts (which must be lowercase). */
export function isDataFilePath(path: string, exts: string[]): boolean {
  const slash = path.lastIndexOf('/');
  const dot = path.lastIndexOf('.');
  if (dot <= slash + 1) return false; // no extension, or a dotfile/dotted dir
  return exts.includes(path.slice(dot + 1).toLowerCase());
}

/**
 * Byte length of a string as UTF-8 — what the size cap is expressed in.
 * `String.length` counts UTF-16 code units, so it understates any non-ASCII
 * file (a Cyrillic CSV would be undercounted by roughly half).
 */
export function utf8ByteLength(s: string): number {
  return new TextEncoder().encode(s).byteLength;
}
