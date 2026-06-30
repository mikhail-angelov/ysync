package ysync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ExecutePlan(ctx context.Context, cfg Config, client *WebDAVClient, state SyncState, plan []PlanItem, log Logger) (SyncState, error) {
	state.LocalRoot = cfg.LocalRoot
	state.RemoteRoot = cfg.RemoteRoot
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Entries == nil {
		state.Entries = map[string]SyncEntry{}
	}

	for _, item := range plan {
		switch item.Action {
		case ActionUpload:
			if err := uploadPath(ctx, cfg.LocalRoot, client, state, item.Path); err != nil {
				markStateError(state, item.Path)
				return state, err
			}
			log.Debug("uploaded", "path", item.Path)
		case ActionDownload:
			if err := downloadPath(ctx, cfg.LocalRoot, client, state, item.Path); err != nil {
				markStateError(state, item.Path)
				return state, err
			}
			log.Debug("downloaded", "path", item.Path)
		case ActionTrashRemote:
			if err := trashRemotePath(ctx, cfg.RemoteRoot, client, state, item.Path); err != nil {
				markStateError(state, item.Path)
				return state, err
			}
			log.Debug("moved remote to trash", "path", item.Path)
		case ActionDeleteRemote:
			if err := deleteRemotePath(ctx, client, state, item.Path); err != nil {
				markStateError(state, item.Path)
				return state, err
			}
			log.Debug("deleted remote", "path", item.Path)
		case ActionTrashLocal:
			if err := trashLocalPath(cfg.LocalRoot, state, item.Path); err != nil {
				markStateError(state, item.Path)
				return state, err
			}
			log.Debug("moved local to trash", "path", item.Path)
		case ActionDeleteLocal:
			if err := deleteLocalPath(cfg.LocalRoot, state, item.Path); err != nil {
				markStateError(state, item.Path)
				return state, err
			}
			log.Debug("deleted local", "path", item.Path)
		case ActionConflict:
			markStateStatus(state, item.Path, StatusConflict)
			NotifyConflict(item.Path, log)
		}
	}
	state.LastSyncedAt = time.Now().UTC()
	return state, nil
}

func uploadPath(ctx context.Context, localRoot string, client *WebDAVClient, state SyncState, relPath string) error {
	relPath = cleanRel(relPath)
	local, err := readLocalEntry(localRoot, relPath)
	if err != nil {
		return err
	}
	if local.IsDir {
		return fmt.Errorf("upload directory %q is not implemented", relPath)
	}

	freshRemote, remoteExists, err := RefreshRemotePath(ctx, client, relPath)
	if err != nil {
		return fmt.Errorf("refresh remote before upload %s: %w", relPath, err)
	}
	if prev, tracked := state.Entries[relPath]; tracked {
		if !remoteExists || remoteDiffers(freshRemote, prev) {
			markStateStatus(state, relPath, StatusConflict)
			return fmt.Errorf("remote changed before upload %s", relPath)
		}
	} else if remoteExists {
		markStateStatus(state, relPath, StatusConflict)
		return fmt.Errorf("remote already exists before upload %s", relPath)
	}

	tempRel := relPath + ".ysync-uploading"
	if err := putLocalFile(ctx, client, filepath.Join(localRoot, filepath.FromSlash(relPath)), tempRel); err != nil {
		return err
	}
	if err := expectWebDAVStatus(client.MoveUploadTempToFinal(ctx, tempRel, relPath)); err != nil {
		return fmt.Errorf("finalize upload %s: %w", relPath, err)
	}

	confirmedRemote, ok, err := RefreshRemotePath(ctx, client, relPath)
	if err != nil {
		return fmt.Errorf("refresh remote after upload %s: %w", relPath, err)
	}
	if !ok {
		return fmt.Errorf("uploaded remote path missing after finalize %s", relPath)
	}
	confirmedLocal, err := readLocalEntry(localRoot, relPath)
	if err != nil {
		return fmt.Errorf("refresh local after upload %s: %w", relPath, err)
	}
	state.Entries[relPath] = EntryFromMetadata(relPath, confirmedLocal, confirmedRemote, StatusSynced)
	return nil
}

func putLocalFile(ctx context.Context, client *WebDAVClient, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := expectWebDAVStatus(client.Put(ctx, remotePath, f)); err != nil {
		return fmt.Errorf("put upload temp %s: %w", remotePath, err)
	}
	return nil
}

func trashRemotePath(ctx context.Context, remoteRoot string, client *WebDAVClient, state SyncState, relPath string) error {
	relPath = cleanRel(relPath)
	if err := ensureRemoteTrashOutsideRoot(remoteRoot); err != nil {
		return err
	}
	ok, err := ensureRemoteUnchangedForDelete(ctx, client, state, relPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// Try MOVE to /ysync-trash/ first (standard WebDAV trash). If Yandex Disk
	// returns 409 Conflict (e.g. destination parent directory doesn't exist,
	// or server doesn't support MOVE to non-existent path), fall back to DELETE.
	resp, err := client.MoveOriginalToTrash(ctx, relPath, remoteTrashPath(relPath))
	if err != nil {
		return fmt.Errorf("move remote to trash %s: %w", relPath, err)
	}
	if resp.StatusCode == http.StatusConflict {
		_ = resp.Body.Close()
		if err := expectWebDAVStatus(client.Delete(ctx, relPath)); err != nil {
			return fmt.Errorf("fallback delete remote %s: %w", relPath, err)
		}
	} else if err := expectWebDAVStatus(resp, nil); err != nil {
		return fmt.Errorf("move remote to trash %s: %w", relPath, err)
	}
	deleteStateSubtree(state, relPath)
	return nil
}

func deleteRemotePath(ctx context.Context, client *WebDAVClient, state SyncState, relPath string) error {
	relPath = cleanRel(relPath)
	ok, err := ensureRemoteUnchangedForDelete(ctx, client, state, relPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := expectWebDAVStatus(client.Delete(ctx, relPath)); err != nil {
		return fmt.Errorf("delete remote %s: %w", relPath, err)
	}
	deleteStateSubtree(state, relPath)
	return nil
}

func ensureRemoteUnchangedForDelete(ctx context.Context, client *WebDAVClient, state SyncState, relPath string) (bool, error) {
	prev, tracked := state.Entries[relPath]

	// For entries already marked as deleted locally (from a paired
	// trash-local/delete-local operation), we only need to check remote
	// existence — skip the metadata comparison that can falsely conflict
	// due to ETag drift (e.g. Yandex Disk returning different ETags on
	// point-refresh). The user explicitly confirmed this deletion.
	if tracked && prev.Status == StatusDeletedLocal {
		remote, ok, err := RefreshRemotePath(ctx, client, relPath)
		if err != nil {
			return false, fmt.Errorf("refresh remote before delete %s: %w", relPath, err)
		}
		if !ok {
			// Remote already gone — nothing to do. Clean up state.
			deleteStateSubtree(state, relPath)
			return false, nil
		}
		// Remote exists — proceed with delete regardless of metadata.
		// The server's HTTP response will validate the actual operation.
		_ = remote
		return true, nil
	}

	remote, ok, err := RefreshRemotePath(ctx, client, relPath)
	if err != nil {
		return false, fmt.Errorf("refresh remote before delete %s: %w", relPath, err)
	}
	if !ok {
		deleteStateSubtree(state, relPath)
		return false, nil
	}
	if tracked && remoteChangedBeforeDelete(remote, prev) {
		markStateStatus(state, relPath, StatusConflict)
		return false, fmt.Errorf("remote changed before delete %s", relPath)
	}
	return true, nil
}

func remoteChangedBeforeDelete(remote RemoteEntry, prev SyncEntry) bool {
	return remote.IsDir != prev.IsDir ||
		remote.Size != prev.RemoteSize ||
		!timeEqual(remote.ModTime, prev.RemoteModTime)
}

func trashLocalPath(localRoot string, state SyncState, relPath string) error {
	relPath = cleanRel(relPath)
	if err := ensureLocalUnchangedForDelete(localRoot, state, relPath); err != nil {
		return err
	}
	src := filepath.Join(localRoot, filepath.FromSlash(relPath))
	dst, err := localTrashPath(relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	// Keep the state entry marked as deleted so the next comparison
	// recognizes this as a tracked deletion and propagates it to the remote.
	markStateStatus(state, relPath, StatusDeletedLocal)
	return nil
}

func deleteLocalPath(localRoot string, state SyncState, relPath string) error {
	relPath = cleanRel(relPath)
	if err := ensureLocalUnchangedForDelete(localRoot, state, relPath); err != nil {
		return err
	}
	target := filepath.Join(localRoot, filepath.FromSlash(relPath))
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.IsDir() {
		err = os.RemoveAll(target)
	} else {
		err = os.Remove(target)
	}
	if err != nil {
		return err
	}
	// Keep the state entry marked as deleted so the next comparison
	// recognizes this as a tracked deletion and propagates it to the remote.
	markStateStatus(state, relPath, StatusDeletedLocal)
	return nil
}

func ensureLocalUnchangedForDelete(localRoot string, state SyncState, relPath string) error {
	prev, tracked := state.Entries[relPath]
	local, err := readLocalEntry(localRoot, relPath)
	if os.IsNotExist(err) {
		// Local file is already gone. Keep the state entry marked as deleted
		// so the next comparison can propagate the deletion to the remote.
		if tracked {
			markStateStatus(state, relPath, StatusDeletedLocal)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if tracked && localDiffers(local, prev) {
		markStateStatus(state, relPath, StatusConflict)
		return fmt.Errorf("local changed before delete %s", relPath)
	}
	return nil
}

func deleteStateSubtree(state SyncState, relPath string) {
	relPath = cleanRel(relPath)
	prefix := relPath + "/"
	for p := range state.Entries {
		if p == relPath || strings.HasPrefix(p, prefix) {
			delete(state.Entries, p)
		}
	}
}

func remoteTrashPath(relPath string) string {
	return "/ysync-trash/" + time.Now().UTC().Format("20060102T150405Z") + "/" + cleanRel(relPath)
}

func ensureRemoteTrashOutsideRoot(remoteRoot string) error {
	root := cleanRemoteRoot(remoteRoot)
	if root == "" {
		// Root-level sync ("/"): /ysync-trash is a valid top-level location.
		return nil
	}
	if root == "ysync-trash" || strings.HasPrefix(root, "ysync-trash/") {
		return fmt.Errorf("remote trash /ysync-trash is inside remote root %q", remoteRoot)
	}
	return nil
}

func localTrashPath(relPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ysync", "trash", time.Now().UTC().Format("20060102T150405Z"), filepath.FromSlash(cleanRel(relPath))), nil
}

func downloadPath(ctx context.Context, localRoot string, client *WebDAVClient, state SyncState, relPath string) error {
	relPath = cleanRel(relPath)
	remote, ok, err := RefreshRemotePath(ctx, client, relPath)
	if err != nil {
		return fmt.Errorf("refresh remote before download %s: %w", relPath, err)
	}
	if !ok {
		return fmt.Errorf("remote path missing before download %s", relPath)
	}
	if remote.IsDir {
		return fmt.Errorf("download directory %q is not implemented", relPath)
	}
	if prev, tracked := state.Entries[relPath]; tracked {
		if local, err := readLocalEntry(localRoot, relPath); err == nil && localDiffers(local, prev) {
			markStateStatus(state, relPath, StatusConflict)
			return fmt.Errorf("local changed before download %s", relPath)
		}
	}

	finalPath := filepath.Join(localRoot, filepath.FromSlash(relPath))
	tempPath := finalPath + ".ysync-downloading"
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		return err
	}
	startAt := int64(0)
	if info, err := os.Stat(tempPath); err == nil {
		startAt = info.Size()
	}
	if err := getRemoteFile(ctx, client, relPath, tempPath, startAt); err != nil {
		return err
	}
	if err := verifyFileSize(tempPath, remote.Size); err != nil {
		return err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return err
	}
	if !remote.ModTime.IsZero() {
		now := time.Now()
		if err := os.Chtimes(finalPath, now, remote.ModTime); err != nil {
			return err
		}
	}
	confirmedLocal, err := readLocalEntry(localRoot, relPath)
	if err != nil {
		return fmt.Errorf("refresh local after download %s: %w", relPath, err)
	}
	state.Entries[relPath] = EntryFromMetadata(relPath, confirmedLocal, remote, StatusSynced)
	return nil
}

func getRemoteFile(ctx context.Context, client *WebDAVClient, relPath, tempPath string, startAt int64) error {
	resp, err := client.Get(ctx, relPath, startAt)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	appendFile := startAt > 0 && resp.StatusCode == http.StatusPartialContent
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("GET %s failed: %s", relPath, resp.Status)
	}
	flag := os.O_CREATE | os.O_WRONLY
	if appendFile {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(tempPath, flag, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return f.Sync()
}

func verifyFileSize(path string, want int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() != want {
		return fmt.Errorf("downloaded size for %s = %d, want %d", path, info.Size(), want)
	}
	return nil
}

func readLocalEntry(localRoot, relPath string) (LocalEntry, error) {
	relPath = cleanRel(relPath)
	info, err := os.Stat(filepath.Join(localRoot, filepath.FromSlash(relPath)))
	if err != nil {
		return LocalEntry{}, err
	}
	return LocalEntry{
		Path:    relPath,
		IsDir:   info.IsDir(),
		ModTime: normalizeTime(info.ModTime()),
		Size:    fileSize(info),
	}, nil
}

func expectWebDAVStatus(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s", resp.Status)
	}
	return nil
}

func markStateError(state SyncState, relPath string) {
	if state.Entries[cleanRel(relPath)].Status == StatusConflict {
		return
	}
	markStateStatus(state, relPath, StatusError)
}

func markStateStatus(state SyncState, relPath, status string) {
	relPath = cleanRel(relPath)
	entry := state.Entries[relPath]
	entry.Path = relPath
	entry.Status = status
	entry.LastSyncedAt = time.Now().UTC()
	state.Entries[relPath] = entry
}
