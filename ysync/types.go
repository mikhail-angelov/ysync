package ysync

import "time"

const Version = "0.1.0"

type LocalEntry struct {
	Path    string
	IsDir   bool
	ModTime time.Time
	Size    int64
}

type RemoteEntry struct {
	Path    string
	IsDir   bool
	ModTime time.Time
	Size    int64
	ETag    string
}

type SyncState struct {
	Version      int                  `json:"version"`
	RemoteRoot   string               `json:"remote_root"`
	LocalRoot    string               `json:"local_root"`
	LastSyncedAt time.Time            `json:"last_synced_at"`
	Entries      map[string]SyncEntry `json:"entries"`
}

type SyncEntry struct {
	Path          string    `json:"path"`
	IsDir         bool      `json:"is_dir"`
	LocalModTime  time.Time `json:"local_mod_time"`
	LocalSize     int64     `json:"local_size"`
	RemoteModTime time.Time `json:"remote_mod_time"`
	RemoteSize    int64     `json:"remote_size"`
	RemoteETag    string    `json:"remote_etag"`
	Status        string    `json:"status"`
	LastSyncedAt  time.Time `json:"last_synced_at"`
}

const (
	StatusSynced          = "synced"
	StatusPendingUpload   = "pending_upload"
	StatusPendingDownload = "pending_download"
	StatusConflict        = "conflict"
	StatusDeletedLocal    = "deleted_local"
	StatusDeletedRemote   = "deleted_remote"
	StatusError           = "error"
)

type Action string

const (
	ActionUpload       Action = "upload"
	ActionDownload     Action = "download"
	ActionConflict     Action = "conflict"
	ActionTrashRemote  Action = "trash-remote"
	ActionTrashLocal   Action = "trash-local"
	ActionDeleteRemote Action = "delete-remote"
	ActionDeleteLocal  Action = "delete-local"
	ActionIgnore       Action = "ignore"
	ActionSkip         Action = "skip"
	ActionMarkSynced   Action = "mark-synced"
	ActionCleanup      Action = "cleanup"
)

type PlanItem struct {
	Action Action
	Path   string
	Reason string
}

func normalizeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Second)
}
