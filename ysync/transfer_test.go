package ysync

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestUploadDirectoryCreatesRemoteDirectoryAndUpdatesState(t *testing.T) {
	localRoot := t.TempDir()
	dir := filepath.Join(localRoot, "audio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var calls []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.Method {
		case "PROPFIND":
			if len(calls) == 1 {
				return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
			}
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/audio/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>
</d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case "MKCOL":
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
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "audio"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"PROPFIND /YSync/audio",
		"MKCOL /YSync/audio",
		"PROPFIND /YSync/audio",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if entry := next.Entries["audio"]; entry.Status != StatusSynced || !entry.IsDir {
		t.Fatalf("bad state entry: %#v", entry)
	}
}

func TestUploadFileCreatesMissingRemoteParents(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(localRoot, "audio/book"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "audio/book/01.mp3"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.Method {
		case "PROPFIND":
			switch req.URL.Path {
			case "/YSync/audio/book/01.mp3":
				if len(calls) == 1 {
					return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
				}
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/audio/book/01.mp3</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			case "/YSync/audio", "/YSync/audio/book":
				return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
			default:
				t.Fatalf("unexpected PROPFIND path: %s", req.URL.Path)
			}
		case "MKCOL":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		case http.MethodPut:
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		case "MOVE":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return nil, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "audio/book/01.mp3"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"PROPFIND /YSync/audio/book/01.mp3",
		"PROPFIND /YSync/audio",
		"MKCOL /YSync/audio",
		"PROPFIND /YSync/audio/book",
		"MKCOL /YSync/audio/book",
		"PUT /YSync/audio/book/01.mp3.ysync-uploading",
		"MOVE /YSync/audio/book/01.mp3.ysync-uploading",
		"PROPFIND /YSync/audio/book/01.mp3",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if entry := next.Entries["audio/book/01.mp3"]; entry.Status != StatusSynced {
		t.Fatalf("bad state entry: %#v", entry)
	}
}

func TestUploadContinuesWhenPutReturnsEOFButTempExists(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.Method {
		case "PROPFIND":
			switch req.URL.Path {
			case "/YSync/a.txt":
				if len(calls) == 1 {
					return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
				}
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			case "/YSync/a.txt.ysync-uploading":
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt.ysync-uploading</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"temp"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			default:
				t.Fatalf("unexpected PROPFIND path: %s", req.URL.Path)
			}
		case http.MethodPut:
			return nil, io.EOF
		case "MOVE":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return nil, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"PROPFIND /YSync/a.txt",
		"PUT /YSync/a.txt.ysync-uploading",
		"PROPFIND /YSync/a.txt.ysync-uploading",
		"MOVE /YSync/a.txt.ysync-uploading",
		"PROPFIND /YSync/a.txt",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if entry := next.Entries["a.txt"]; entry.Status != StatusSynced {
		t.Fatalf("bad state entry: %#v", entry)
	}
}

func TestUploadRetriesPutWhenTempMissingAfterEOF(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	putAttempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.Method {
		case "PROPFIND":
			switch req.URL.Path {
			case "/YSync/a.txt":
				if len(calls) == 1 {
					return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
				}
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			case "/YSync/a.txt.ysync-uploading":
				return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
			default:
				t.Fatalf("unexpected PROPFIND path: %s", req.URL.Path)
			}
		case http.MethodPut:
			putAttempts++
			if putAttempts == 1 {
				return nil, io.EOF
			}
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		case "MOVE":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return nil, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	if _, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if putAttempts != 2 {
		t.Fatalf("put attempts = %d, want 2", putAttempts)
	}
}

func TestUploadVerifiesTempWhenPutResponseHangsAfterBodySent(t *testing.T) {
	oldGrace := uploadResponseGrace
	uploadResponseGrace = 10 * time.Millisecond
	t.Cleanup(func() { uploadResponseGrace = oldGrace })

	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.Method {
		case "PROPFIND":
			switch req.URL.Path {
			case "/YSync/a.txt":
				if len(calls) == 1 {
					return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
				}
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			case "/YSync/a.txt.ysync-uploading":
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt.ysync-uploading</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"temp"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			default:
				t.Fatalf("unexpected PROPFIND path: %s", req.URL.Path)
			}
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, req.Body)
			<-req.Context().Done()
			return nil, req.Context().Err()
		case "MOVE":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return nil, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"PROPFIND /YSync/a.txt",
		"PUT /YSync/a.txt.ysync-uploading",
		"PROPFIND /YSync/a.txt.ysync-uploading",
		"MOVE /YSync/a.txt.ysync-uploading",
		"PROPFIND /YSync/a.txt",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if entry := next.Entries["a.txt"]; entry.Status != StatusSynced {
		t.Fatalf("bad state entry: %#v", entry)
	}
}

func TestUploadRetriesWhenResponseTimeoutLeavesNoTemp(t *testing.T) {
	oldGrace := uploadResponseGrace
	uploadResponseGrace = 10 * time.Millisecond
	t.Cleanup(func() { uploadResponseGrace = oldGrace })

	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	putAttempts := 0
	tempChecks := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			switch req.URL.Path {
			case "/YSync/a.txt":
				if putAttempts == 0 {
					return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
				}
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			case "/YSync/a.txt.ysync-uploading":
				tempChecks++
				return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
			default:
				t.Fatalf("unexpected PROPFIND path: %s", req.URL.Path)
			}
		case http.MethodPut:
			putAttempts++
			if putAttempts == 1 {
				_, _ = io.Copy(io.Discard, req.Body)
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		case "MOVE":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return nil, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	next, err := ExecutePlan(context.Background(), cfg, client, emptyState(), []PlanItem{{Action: ActionUpload, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if putAttempts != 2 {
		t.Fatalf("put attempts = %d, want 2", putAttempts)
	}
	if tempChecks != 1 {
		t.Fatalf("temp checks = %d, want 1", tempChecks)
	}
	if entry := next.Entries["a.txt"]; entry.Status != StatusSynced {
		t.Fatalf("bad state entry: %#v", entry)
	}
}

func TestExecutePlanDetailedContinuesAfterIndependentUploadFailure(t *testing.T) {
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "b.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	bPropfinds := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			switch req.URL.Path {
			case "/YSync/a.txt":
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>9</d:getcontentlength><d:getetag>"remote"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			case "/YSync/b.txt":
				bPropfinds++
				if bPropfinds == 1 {
					return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
				}
				body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/b.txt</d:href><d:propstat><d:prop><d:getcontentlength>5</d:getcontentlength><d:getetag>"fresh"</d:getetag></d:prop></d:propstat></d:response>
</d:multistatus>`
				return xmlResponse(http.StatusMultiStatus, body), nil
			}
		case http.MethodPut, "MOVE":
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return textResponse(http.StatusNotFound, "404 Not Found", ""), nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	state, executed, err := ExecutePlanDetailed(context.Background(), cfg, client, emptyState(), []PlanItem{
		{Action: ActionUpload, Path: "a.txt"},
		{Action: ActionUpload, Path: "b.txt"},
	}, Logger{out: io.Discard})
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if len(executed) != 1 || executed[0].Path != "b.txt" {
		t.Fatalf("executed = %#v, want only b.txt", executed)
	}
	if got := state.Entries["a.txt"].Status; got != StatusConflict {
		t.Fatalf("a status = %q, want conflict", got)
	}
	if got := state.Entries["b.txt"].Status; got != StatusSynced {
		t.Fatalf("b status = %q, want synced", got)
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

func TestTrashRemoteFallsBackToDeleteOn409Conflict(t *testing.T) {
	var gotMethod string
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
			gotMethod = "MOVE"
			// Yandex Disk returns 409 Conflict when destination directory doesn't exist
			return textResponse(http.StatusConflict, "409 Conflict", ""), nil
		case "DELETE":
			gotMethod = "DELETE"
			return textResponse(http.StatusNoContent, "204 No Content", ""), nil
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
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		RemoteSize:    5,
		RemoteModTime: at(1),
		RemoteETag:    "old",
		Status:        StatusSynced,
	}
	cfg := Config{LocalRoot: t.TempDir(), RemoteRoot: "/YSync"}
	_, err = ExecutePlan(context.Background(), cfg, client, state, []PlanItem{{Action: ActionTrashRemote, Path: "a.txt"}}, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("expected DELETE fallback on 409, got %s", gotMethod)
	}
}

func TestTrashLocalThenTrashRemoteSucceedsWhenRemoteMetadataDrifted(t *testing.T) {
	localRoot := t.TempDir()
	path := filepath.Join(localRoot, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var destinations []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			// Return a **different** ETag than what's in state, simulating
			// Yandex Disk metadata drift. Without the fix, this would cause
			// remoteDiffers to return true and block the remote deletion.
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Thu, 01 Jan 1970 00:00:02 GMT</d:getlastmodified>
<d:getcontentlength>5</d:getcontentlength><d:getetag>"drifted-etag"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case "MOVE":
			destinations = append(destinations, req.Header.Get("Destination"))
			// Simulate Yandex Disk 409, expecting fallback to DELETE
			if strings.Contains(req.Header.Get("Destination"), "ysync-trash") {
				return textResponse(http.StatusConflict, "409 Conflict", ""), nil
			}
			return textResponse(http.StatusCreated, "201 Created", ""), nil
		case "DELETE":
			// Fallback DELETE should be called after MOVE 409
			return textResponse(http.StatusNoContent, "204 No Content", ""), nil
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
	state.Entries["a.txt"] = SyncEntry{
		Path:          "a.txt",
		LocalSize:     5,
		LocalModTime:  normalizeTime(info.ModTime()),
		RemoteSize:    5,
		RemoteModTime: at(1),
		RemoteETag:    "old-etag",
		Status:        StatusSynced,
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	plan := []PlanItem{
		{Action: ActionTrashLocal, Path: "a.txt", Reason: "user requested from UI"},
		{Action: ActionTrashRemote, Path: "a.txt", Reason: "paired remote trash for local delete"},
	}
	next, err := ExecutePlan(context.Background(), cfg, client, state, plan, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists or unexpected stat error: %v", err)
	}
	if _, ok := next.Entries["a.txt"]; ok {
		t.Fatalf("state entry was not removed after paired plan: %#v", next.Entries["a.txt"])
	}
}

func TestTrashRemoteAllowsETagOnlyDriftForLocalDeleteSync(t *testing.T) {
	var gotDelete bool
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop>
<d:getlastmodified>Thu, 01 Jan 1970 00:00:01 GMT</d:getlastmodified>
<d:getcontentlength>5</d:getcontentlength><d:getetag>"drifted"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case "MOVE":
			return textResponse(http.StatusConflict, "409 Conflict", ""), nil
		case "DELETE":
			gotDelete = true
			return textResponse(http.StatusNoContent, "204 No Content", ""), nil
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
	if !gotDelete {
		t.Fatal("expected DELETE fallback after MOVE conflict")
	}
	if _, ok := next.Entries["a.txt"]; ok {
		t.Fatalf("state entry was not removed after remote delete: %#v", next.Entries["a.txt"])
	}
}

func TestDeleteLocalThenDeleteRemoteSucceedsWhenRemoteMetadataDrifted(t *testing.T) {
	localRoot := t.TempDir()
	dir := filepath.Join(localRoot, "mydir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	var gotDeletePath string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "PROPFIND":
			body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/mydir</d:href><d:propstat><d:prop>
<d:getlastmodified>Thu, 01 Jan 1970 00:00:02 GMT</d:getlastmodified>
<d:getcontentlength>0</d:getcontentlength><d:getetag>"drifted"</d:getetag>
</d:prop></d:propstat></d:response></d:multistatus>`
			return xmlResponse(http.StatusMultiStatus, body), nil
		case "DELETE":
			gotDeletePath = req.URL.Path
			return textResponse(http.StatusNoContent, "204 No Content", ""), nil
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
	state.Entries["mydir"] = SyncEntry{
		Path:          "mydir",
		IsDir:         true,
		LocalModTime:  normalizeTime(dirInfo.ModTime()),
		RemoteModTime: at(1),
		RemoteETag:    "old-etag",
		Status:        StatusSynced,
	}
	cfg := Config{LocalRoot: localRoot, RemoteRoot: "/YSync"}
	plan := []PlanItem{
		{Action: ActionDeleteLocal, Path: "mydir", Reason: "user requested from UI"},
		{Action: ActionDeleteRemote, Path: "mydir", Reason: "paired remote delete for local delete"},
	}
	next, err := ExecutePlan(context.Background(), cfg, client, state, plan, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists or unexpected stat error: %v", err)
	}
	if !strings.HasSuffix(gotDeletePath, "/mydir") {
		t.Fatalf("DELETE path = %q, want .../mydir", gotDeletePath)
	}
	if _, ok := next.Entries["mydir"]; ok {
		t.Fatalf("state entry was not removed after paired plan: %#v", next.Entries["mydir"])
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
