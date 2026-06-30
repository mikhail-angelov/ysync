package ysync

import (
	"sort"
	"time"
)

const timestampTolerance = 30 * time.Second

type CompareOptions struct {
	ImportRemote bool
	InitStrategy string
	Delete       bool
	DeleteMode   string
}

func Compare(state SyncState, locals map[string]LocalEntry, remotes map[string]RemoteEntry, opts CompareOptions) []PlanItem {
	scoped := map[string]struct{}{}
	for path := range locals {
		scoped[path] = struct{}{}
	}
	for path := range state.Entries {
		scoped[path] = struct{}{}
	}
	if opts.ImportRemote {
		for path, remote := range remotes {
			if !isServiceRemoteTemp(path, remote.IsDir) {
				scoped[path] = struct{}{}
				continue
			}
			scoped[path] = struct{}{}
		}
	}

	paths := make([]string, 0, len(scoped))
	for path := range scoped {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var plan []PlanItem
	for _, path := range paths {
		local, hasLocal := locals[path]
		remote, hasRemote := remotes[path]
		prev, tracked := state.Entries[path]

		if hasRemote && isServiceRemoteTemp(path, remote.IsDir) {
			plan = append(plan, PlanItem{Action: ActionIgnore, Path: path, Reason: "service remote temp file"})
			continue
		}
		if !tracked {
			plan = append(plan, compareUntracked(path, local, hasLocal, remote, hasRemote, opts)...)
			continue
		}
		plan = append(plan, compareTracked(path, prev, local, hasLocal, remote, hasRemote, opts))
	}

	if !opts.ImportRemote {
		for path, remote := range remotes {
			if _, ok := scoped[path]; ok {
				continue
			}
			if isServiceRemoteTemp(path, remote.IsDir) {
				if opts.ImportRemote {
					continue
				}
				plan = append(plan, PlanItem{Action: ActionIgnore, Path: path, Reason: "service remote temp file"})
				continue
			}
			plan = append(plan, PlanItem{Action: ActionIgnore, Path: path, Reason: "remote-only untracked file"})
		}
		sort.SliceStable(plan, func(i, j int) bool {
			if plan[i].Path == plan[j].Path {
				return plan[i].Action < plan[j].Action
			}
			return plan[i].Path < plan[j].Path
		})
	}
	return plan
}

func compareUntracked(path string, local LocalEntry, hasLocal bool, remote RemoteEntry, hasRemote bool, opts CompareOptions) []PlanItem {
	switch {
	case hasLocal && !hasRemote:
		return []PlanItem{{Action: ActionUpload, Path: path, Reason: "local new file"}}
	case !hasLocal && hasRemote:
		if opts.ImportRemote && !isServiceRemoteTemp(path, remote.IsDir) {
			return []PlanItem{{Action: ActionDownload, Path: path, Reason: "remote import enabled"}}
		}
		return []PlanItem{{Action: ActionIgnore, Path: path, Reason: "remote-only untracked file"}}
	case hasLocal && hasRemote:
		if sameInitial(local, remote) {
			return []PlanItem{{Action: ActionMarkSynced, Path: path, Reason: "first run matching metadata"}}
		}
		switch opts.InitStrategy {
		case "local-wins":
			return []PlanItem{{Action: ActionUpload, Path: path, Reason: "first run local-wins"}}
		case "remote-wins":
			return []PlanItem{{Action: ActionDownload, Path: path, Reason: "first run remote-wins"}}
		case "newer-wins":
			if normalizeTime(local.ModTime).After(normalizeTime(remote.ModTime)) {
				return []PlanItem{{Action: ActionUpload, Path: path, Reason: "first run local newer"}}
			}
			if normalizeTime(remote.ModTime).After(normalizeTime(local.ModTime)) {
				return []PlanItem{{Action: ActionDownload, Path: path, Reason: "first run remote newer"}}
			}
		}
		return []PlanItem{{Action: ActionConflict, Path: path, Reason: "first run differing metadata"}}
	default:
		return nil
	}
}

func compareTracked(path string, prev SyncEntry, local LocalEntry, hasLocal bool, remote RemoteEntry, hasRemote bool, opts CompareOptions) PlanItem {
	localDeleted := !hasLocal
	remoteDeleted := !hasRemote
	localChanged := hasLocal && localDiffers(local, prev)
	remoteChanged := hasRemote && remoteDiffers(remote, prev)

	switch {
	case localDeleted && remoteDeleted:
		return PlanItem{Action: ActionSkip, Path: path, Reason: "deleted on both sides"}
	case localDeleted && remoteChanged:
		return PlanItem{Action: ActionConflict, Path: path, Reason: "local deleted and remote changed"}
	case remoteDeleted && localChanged:
		return PlanItem{Action: ActionConflict, Path: path, Reason: "remote deleted and local changed"}
	case localDeleted:
		if !opts.Delete {
			return PlanItem{Action: ActionSkip, Path: path, Reason: "local delete sync disabled"}
		}
		if opts.DeleteMode == "hard" {
			return PlanItem{Action: ActionDeleteRemote, Path: path, Reason: "tracked file deleted locally"}
		}
		return PlanItem{Action: ActionTrashRemote, Path: path, Reason: "tracked file deleted locally"}
	case remoteDeleted:
		if !opts.Delete {
			return PlanItem{Action: ActionSkip, Path: path, Reason: "remote delete sync disabled"}
		}
		if opts.DeleteMode == "hard" {
			return PlanItem{Action: ActionDeleteLocal, Path: path, Reason: "tracked file deleted remotely"}
		}
		return PlanItem{Action: ActionTrashLocal, Path: path, Reason: "tracked file deleted remotely"}
	case localChanged && remoteChanged:
		return PlanItem{Action: ActionConflict, Path: path, Reason: "changed locally and remotely"}
	case localChanged:
		return PlanItem{Action: ActionUpload, Path: path, Reason: "tracked local changed"}
	case remoteChanged:
		return PlanItem{Action: ActionDownload, Path: path, Reason: "tracked remote changed"}
	default:
		return PlanItem{Action: ActionSkip, Path: path, Reason: "unchanged"}
	}
}

func localDiffers(local LocalEntry, prev SyncEntry) bool {
	return local.IsDir != prev.IsDir ||
		local.Size != prev.LocalSize ||
		!timeEqual(local.ModTime, prev.LocalModTime)
}

func remoteDiffers(remote RemoteEntry, prev SyncEntry) bool {
	if remote.IsDir != prev.IsDir || remote.Size != prev.RemoteSize {
		return true
	}
	if remote.ETag != "" && prev.RemoteETag != "" && remote.ETag != prev.RemoteETag {
		return true
	}
	return !timeEqual(remote.ModTime, prev.RemoteModTime)
}

func sameInitial(local LocalEntry, remote RemoteEntry) bool {
	return local.IsDir == remote.IsDir &&
		local.Size == remote.Size
}

func timeEqual(a, b time.Time) bool {
	a = normalizeTime(a)
	b = normalizeTime(b)
	if a.IsZero() || b.IsZero() {
		return a.Equal(b)
	}
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= timestampTolerance
}

func isServiceRemoteTemp(path string, isDir bool) bool {
	if isDir {
		return false
	}
	return matchIgnorePattern("*.ysync-uploading", cleanRel(path), cleanRel(path), false)
}
