package ysync

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadUsesRemoteTempMoveAndUpdatesStateAfterRefresh(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls []string
	var uploaded string
	propfinds := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.Method {
		case "PROPFIND":
			propfinds++
			if propfinds == 1 {
				return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
			}
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Sun, 28 Jun 2026 10:00:00 GMT</d:getlastmodified>
<d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case http.MethodPut:
			data, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			uploaded = string(data)
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		case "MOVE":
			if got := req.Header.Get("Overwrite"); got != "T" {
				t.Fatalf("MOVE Overwrite = %q, want T", got)
			}
			if got := req.Header.Get("Destination"); got != "https://example.test/YSync/a.txt" {
				t.Fatalf("Destination = %q", got)
			}
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, state, []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"PROPFIND /YSync/a.txt",
		"PUT /YSync/a.txt.ysync-uploading",
		"MOVE /YSync/a.txt.ysync-uploading",
		"PROPFIND /YSync/a.txt",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if uploaded != "hello" {
		t.Fatalf("uploaded = %q", uploaded)
	}
	entry := next.Entries["a.txt"]
	if entry.Status != StatusSynced || entry.RemoteETag != "fresh" || entry.RemoteSize != 5 {
		t.Fatalf("bad state entry: %#v", entry)
	}
}

func TestUploadConflictsWhenUntrackedRemoteExistsAtPointRefresh(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "PROPFIND" {
			t.Fatalf("unexpected request after conflict: %s", req.Method)
		}
		body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Sun, 28 Jun 2026 10:00:02 GMT</d:getlastmodified>
<d:getcontentlength>9</d:getcontentlength><d:getetag>"remote"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
		return xmlResponse(http.StatusMultiStatus, body), nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if got := next.Entries["a.txt"].Status; got != StatusConflict {
		t.Fatalf("status = %q, want %q", got, StatusConflict)
	}
}

func TestUploadConflictsWhenTrackedRemoteChangedAtPointRefresh(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "PROPFIND" {
			t.Fatalf("unexpected request after conflict: %s", req.Method)
		}
		body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Sun, 28 Jun 2026 10:00:02 GMT</d:getlastmodified>
<d:getcontentlength>9</d:getcontentlength><d:getetag>"new"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
		return xmlResponse(http.StatusMultiStatus, body), nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		LocalSize:     5,
		LocalModTime:  at(1),
		RemoteSize:    5,
		RemoteModTime: at(1),
		RemoteETag:    "old",
		Status:        StatusSynced,
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, state, []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if got := next.Entries["a.txt"].Status; got != StatusConflict {
		t.Fatalf("status = %q, want %q", got, StatusConflict)
	}
}

func TestDownloadResumesTempFileAndUpdatesState(t *testing.T) {
	localRoot := t.TempDir()
	finalPath := filepath.Join(localRoot, "a.txt")
	if err := os.WriteFile(finalPath+".ysync-downloading", []byte("he"), 0o600); err != nil {
		t.Fatal(err)
	}

	var rangeHeader string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Sun, 28 Jun 2026 10:00:00 GMT</d:getlastmodified>
<d:getcontentlength>5</d:getcontentlength><d:getetag>"remote"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case http.MethodGet:
			rangeHeader = req.Header.Get("Range")
			return &http.Response{StatusCode: http.StatusPartialContent, Status: "206 Partial Content", Body: io.NopCloser(strings.NewReader("llo")), Header: http.Header{}}, nil
		default:
			t.Fatalf("unexpected request: %s", req.Method)
			return nil, nil
		}
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionDownload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "bytes=2-" {
		t.Fatalf("Range = %q, want bytes=2-", rangeHeader)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("downloaded = %q", data)
	}
	if next.Entries["a.txt"].Status != StatusSynced {
		t.Fatalf("state entry = %#v", next.Entries["a.txt"])
	}
}

func TestTrashRemoteRefreshesPointAndMovesOutsideSyncRoot(t *testing.T) {
	var destination string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Thu, 01 Jan 1970 00:00:01 GMT</d:getlastmodified>
<d:getcontentlength>5</d:getcontentlength><d:getetag>"old"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case "MOVE":
			destination = req.Header.Get("Destination")
			if got := req.Header.Get("Overwrite"); got != "F" {
				t.Fatalf("Overwrite = %q, want F", got)
			}
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s", req.Method)
			return nil, nil
		}
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		RemoteSize:    5,
		RemoteModTime: at(1),
		RemoteETag:    "old",
		Status:        StatusSynced,
	}
	cfg := Config{LocalRoot: t.TempDir(), RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, state, []PlanItem{{Action: ActionTrashRemote, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(destination, "https://example.test/ysync-trash/") {
		t.Fatalf("Destination = %q, want outside /", destination)
	}
	if _, ok := next.Entries["a.txt"]; ok {
		t.Fatalf("state entry was not removed: %#v", next.Entries["a.txt"])
	}
}

func TestDeleteLocalRemovesUnchangedTrackedFileAndPreservesStateEntryForRemotePropagation(t *testing.T) {
	localRoot := t.TempDir()
	path := filepath.Join(localRoot, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	state.Entries["a.txt"] = SyncEntry{
		Path:         "a.txt",
		LocalSize:    5,
		LocalModTime: normalizeTime(info.ModTime()),
		Status:       StatusSynced,
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, nil, state, []PlanItem{{Action: ActionDeleteLocal, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists or unexpected stat error: %v", err)
	}
	// State entry must be preserved (not removed) so the next comparison
	// recognizes this as a tracked deletion and propagates it to the remote.
	entry, ok := next.Entries["a.txt"]
	if !ok {
		t.Fatal("state entry was removed; should be preserved with StatusDeletedLocal for remote propagation")
	}
	if entry.Status != StatusDeletedLocal {
		t.Fatalf("state entry status = %q, want %q", entry.Status, StatusDeletedLocal)
	}
}

func TestDeleteLocalRemovesNonEmptyDirectoryAndPreservesDirectoryStateEntryForRemotePropagation(t *testing.T) {
	localRoot := t.TempDir()
	dir := filepath.Join(localRoot, "docs")
	child := filepath.Join(dir, "a.txt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	state.Entries["docs"] = SyncEntry{
		Path:         "docs",
		IsDir:        true,
		LocalModTime: normalizeTime(dirInfo.ModTime()),
		Status:       StatusSynced,
	}
	state.Entries["docs/a.txt"] = SyncEntry{
		Path:         "docs/a.txt",
		LocalSize:    5,
		LocalModTime: normalizeTime(dirInfo.ModTime()),
		Status:       StatusSynced,
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, nil, state, []PlanItem{{Action: ActionDeleteLocal, Path: "docs"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists or unexpected stat error: %v", err)
	}
	// The directory state entry must be preserved (not removed) so the next comparison
	// recognizes this as a tracked deletion and propagates it to the remote.
	entry, ok := next.Entries["docs"]
	if !ok {
		t.Fatal("directory state entry was removed; should be preserved with StatusDeletedLocal for remote propagation")
	}
	if entry.Status != StatusDeletedLocal {
		t.Fatalf("directory state entry status = %q, want %q", entry.Status, StatusDeletedLocal)
	}
}

func TestDeleteLocalAllowsUntrackedDirectoryFromUIAction(t *testing.T) {
	localRoot := t.TempDir()
	dir := filepath.Join(localRoot, "local-only")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	_, err := ExecutePlan(context.Background(), cfg, nil, state, []PlanItem{{Action: ActionDeleteLocal, Path: "local-only"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists or unexpected stat error: %v", err)
	}
}

func xmlResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func textResponse(status int, statusText, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: statusText, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
