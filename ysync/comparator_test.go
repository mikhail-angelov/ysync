package ysync

import (
	"testing"
	"time"
)

func TestCompareRemoteOnlyUntrackedIgnoredByDefault(t *testing.T) {
	plan := Compare(emptyState(), nil, map[string]RemoteEntry{
		"remote.txt": {Path: "remote.txt", Size: 12},
	}, defaultCompareOptions())
	assertAction(t, plan, "remote.txt", ActionIgnore)
}

func TestCompareImportRemoteDoesNotImportServiceTemp(t *testing.T) {
	opts := defaultCompareOptions()
	opts.ImportRemote = true
	plan := Compare(emptyState(), nil, map[string]RemoteEntry{
		"a.txt.ysync-uploading": {Path: "a.txt.ysync-uploading", Size: 12},
	}, opts)
	assertAction(t, plan, "a.txt.ysync-uploading", ActionIgnore)
}

func TestCompareLocalOnlyUntrackedUploads(t *testing.T) {
	plan := Compare(emptyState(), map[string]LocalEntry{
		"a.txt": {Path: "a.txt", Size: 1},
	}, nil, defaultCompareOptions())
	assertAction(t, plan, "a.txt", ActionUpload)
}

func TestCompareTrackedRemoteChangeDownloads(t *testing.T) {
	state := emptyState()
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		LocalSize:     1,
		LocalModTime:  at(1),
		RemoteSize:    1,
		RemoteModTime: at(1),
		RemoteETag:    "old",
		Status:        StatusSynced,
	}
	plan := Compare(state,
		map[string]LocalEntry{"a.txt": {Path: "a.txt", Size: 1, ModTime: at(1)}},
		map[string]RemoteEntry{"a.txt": {Path: "a.txt", Size: 2, ModTime: at(2), ETag: "new"}},
		defaultCompareOptions())
	assertAction(t, plan, "a.txt", ActionDownload)
}

func TestCompareBothSidesChangedConflicts(t *testing.T) {
	state := emptyState()
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		LocalSize:     1,
		LocalModTime:  at(1),
		RemoteSize:    1,
		RemoteModTime: at(1),
		RemoteETag:    "old",
		Status:        StatusSynced,
	}
	plan := Compare(state,
		map[string]LocalEntry{"a.txt": {Path: "a.txt", Size: 3, ModTime: at(3)}},
		map[string]RemoteEntry{"a.txt": {Path: "a.txt", Size: 2, ModTime: at(2), ETag: "new"}},
		defaultCompareOptions())
	assertAction(t, plan, "a.txt", ActionConflict)
}

func TestCompareLocalDeleteMovesTrackedRemoteToTrash(t *testing.T) {
	state := emptyState()
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		LocalSize:     1,
		LocalModTime:  at(1),
		RemoteSize:    1,
		RemoteModTime: at(1),
		RemoteETag:    "etag",
		Status:        StatusSynced,
	}
	plan := Compare(state, nil,
		map[string]RemoteEntry{"a.txt": {Path: "a.txt", Size: 1, ModTime: at(1), ETag: "etag"}},
		defaultCompareOptions())
	assertAction(t, plan, "a.txt", ActionTrashRemote)
}

func TestCompareFirstRunDifferingFilesConflictByDefault(t *testing.T) {
	plan := Compare(emptyState(),
		map[string]LocalEntry{"a.txt": {Path: "a.txt", Size: 1, ModTime: at(1)}},
		map[string]RemoteEntry{"a.txt": {Path: "a.txt", Size: 2, ModTime: at(2)}},
		defaultCompareOptions())
	assertAction(t, plan, "a.txt", ActionConflict)
}

func TestCompareFirstRunSameSizeIgnoresTimestamp(t *testing.T) {
	plan := Compare(emptyState(),
		map[string]LocalEntry{"a.txt": {Path: "a.txt", Size: 1, ModTime: at(1)}},
		map[string]RemoteEntry{"a.txt": {Path: "a.txt", Size: 1, ModTime: at(1000)}},
		defaultCompareOptions())
	assertAction(t, plan, "a.txt", ActionMarkSynced)
}

func TestTimeEqualAllowsThirtySecondTolerance(t *testing.T) {
	if !timeEqual(at(10), at(40)) {
		t.Fatal("expected thirty second difference to be equal")
	}
	if timeEqual(at(10), at(41)) {
		t.Fatal("expected thirty one second difference to differ")
	}
}

func emptyState() SyncState {
	return SyncState{Version: 1, Entries: map[string]SyncEntry{}}
}

func defaultCompareOptions() CompareOptions {
	return CompareOptions{InitStrategy: "skip-conflicts", Delete: true, DeleteMode: "trash"}
}

func at(sec int64) time.Time {
	return time.Unix(sec, 0).UTC()
}

func assertAction(t *testing.T, plan []PlanItem, path string, action Action) {
	t.Helper()
	for _, item := range plan {
		if item.Path == path {
			if item.Action != action {
				t.Fatalf("%s action = %s, want %s; full plan: %#v", path, item.Action, action, plan)
			}
			return
		}
	}
	t.Fatalf("path %q not found in plan %#v", path, plan)
}
