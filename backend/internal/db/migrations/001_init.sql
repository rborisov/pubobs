CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    is_instance_admin INTEGER NOT NULL DEFAULT 0,
    is_banned         INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS groups (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  TEXT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS repos (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    remote_url      TEXT NOT NULL,
    encrypted_creds TEXT NOT NULL,
    default_branch  TEXT NOT NULL DEFAULT 'main',
    local_path      TEXT,
    cloned_at       DATETIME,
    last_used_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS repo_access (
    id             TEXT PRIMARY KEY,
    repo_id        TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group')),
    principal_id   TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('reader','commentator','editor','admin')),
    UNIQUE (repo_id, principal_type, principal_id)
);

CREATE TABLE IF NOT EXISTS notes (
    id         TEXT PRIMARY KEY,
    repo_id    TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (repo_id, path)
);

CREATE TABLE IF NOT EXISTS note_snapshots (
    id             TEXT PRIMARY KEY,
    note_id        TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    html_content   TEXT NOT NULL,
    metadata_json  TEXT NOT NULL DEFAULT '{}',
    synced_by      TEXT NOT NULL REFERENCES users(id),
    git_commit_sha TEXT NOT NULL,
    synced_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (note_id)
);

CREATE TABLE IF NOT EXISTS comments (
    id         TEXT PRIMARY KEY,
    note_id    TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id),
    parent_id  TEXT REFERENCES comments(id),
    body       TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS note_links (
    source_note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    target_path    TEXT NOT NULL,
    PRIMARY KEY (source_note_id, target_path)
);

CREATE TABLE IF NOT EXISTS folder_mappings (
    user_id        TEXT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    repo_id        TEXT NOT NULL REFERENCES repos(id)  ON DELETE CASCADE,
    vault_folder   TEXT NOT NULL,
    repo_subfolder TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, repo_id)
);

CREATE TABLE IF NOT EXISTS system_health (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    disk_free_pct    REAL    NOT NULL DEFAULT 100,
    disk_free_bytes  INTEGER NOT NULL DEFAULT 0,
    disk_status      TEXT    NOT NULL DEFAULT 'ok',
    last_eviction_at DATETIME,
    checked_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS registration_allowlist (
    id         TEXT PRIMARY KEY,
    pattern    TEXT UNIQUE NOT NULL, -- exact email or @domain.com suffix
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
