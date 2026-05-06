package model

import "time"

type User struct {
	ID              string    `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	IsInstanceAdmin bool      `json:"is_instance_admin"`
	IsBanned        bool      `json:"is_banned"`
	CreatedAt       time.Time `json:"created_at"`
}

type AllowlistEntry struct {
	ID        string    `json:"id"`
	Pattern   string    `json:"pattern"`
	CreatedAt time.Time `json:"created_at"`
}

type Group struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

type Repo struct {
	ID             string
	Name           string
	RemoteURL      string
	EncryptedCreds string
	DefaultBranch  string
	LocalPath      *string
	ClonedAt       *time.Time
	LastUsedAt     *time.Time
	CreatedAt      time.Time
	AllowGuest     bool
}

type RepoAccess struct {
	ID            string
	RepoID        string
	PrincipalType string // "user" | "group"
	PrincipalID   string
	Role          string // "reader" | "commentator" | "editor" | "admin"
}

type Note struct {
	ID        string
	RepoID    string
	Path      string
	UpdatedAt time.Time
}

type NoteSnapshot struct {
	ID           string
	NoteID       string
	HTMLContent  string
	MetadataJSON string
	SyncedBy     string
	GitCommitSHA string
	SyncedAt     time.Time
}

type Comment struct {
	ID        string
	NoteID    string
	UserID    string
	ParentID  *string
	Body      string
	CreatedAt time.Time
}

type NoteLink struct {
	SourceNoteID string
	TargetPath   string
}

type FolderMapping struct {
	UserID        string
	RepoID        string
	VaultFolder   string
	RepoSubfolder string
}

type SystemHealth struct {
	ID             int
	DiskFreePct    float64
	DiskFreeBytes  int64
	DiskStatus     string // "ok" | "warn" | "crit"
	LastEvictionAt *time.Time
	CheckedAt      time.Time
}

// Commit is a git log entry returned by the history API.
type Commit struct {
	SHA         string    `json:"sha"`
	AuthorEmail string    `json:"author_email"`
	AuthorName  string    `json:"author_name"`
	Date        time.Time `json:"date"`
	Message     string    `json:"message"`
}

// FileEntry is a file path + content + blob SHA returned by the files API.
type FileEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

var roleOrder = map[string]int{
	"reader": 1, "commentator": 2, "editor": 3, "admin": 4,
}

// RoleAtLeast returns true if userRole satisfies the required minimum role.
func RoleAtLeast(userRole, required string) bool {
	return roleOrder[userRole] >= roleOrder[required]
}
