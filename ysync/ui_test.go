package ysync

import (
	"bytes"
	"context"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUITemplatesParse(t *testing.T) {
	_, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTreeFolderRowIsClickable(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{Nodes: []UITreeNode{{
		Path:        "Documents",
		Name:        "Documents",
		NodeID:      "node-Documents",
		IsDir:       true,
		Status:      "local-only",
		LocalExists: true,
		ChildrenURL: "/tree?path=Documents&rows=1",
	}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "treeRows", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`class="finder-row folder"`,
		`hx-get="/tree?path=Documents&amp;rows=1"`,
		`hx-trigger="click"`,
		`hx-target="#children-node-Documents"`,
		`<span class="finder-chevron"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("tree row HTML missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `<button class="finder-chevron"`) {
		t.Fatalf("folder row should not depend on clicking a chevron button:\n%s", html)
	}
}

func TestTreeRowShowsStatusIconAndActions(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{Nodes: []UITreeNode{{
		Path:         "a.txt",
		Name:         "a.txt",
		NodeID:       "node-a-txt",
		Status:       "remote-only-ignored",
		RemoteExists: true,
	}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "treeRows", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`id="entry-node-a-txt"`,
		`id="row-node-a-txt"`,
		`finder-status-icon status-remote-only-ignored`,
		`class="sync-form"`,
		`hx-post="/actions/path/download?path=a.txt&amp;return=tree-row"`,
		`hx-target="#entry-node-a-txt"`,
		`hx-swap="outerHTML"`,
		`<input type="hidden" name="return" value="tree-row">`,
		`hx-get="/partials/confirm?action=trash-remote&path=a.txt&return=tree-row"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("tree row HTML missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(uiTemplates, `<span>Status</span>`) {
		t.Fatal("tree header should not include a separate Status column")
	}
	if strings.Contains(html, `hx-post="/actions/path/open-local"`) {
		t.Fatalf("remote-only file should not show Open:\n%s", html)
	}
}

func TestTreeLocalOpenDoesNotSwapUI(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{Nodes: []UITreeNode{{
		Path:         "local.txt",
		Name:         "local.txt",
		NodeID:       "node-local-txt",
		Status:       "synced",
		LocalExists:  true,
		RemoteExists: true,
	}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "treeRows", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`hx-post="/actions/path/open-local"`,
		`hx-target="#noop"`,
		`hx-swap="none"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("tree row HTML missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `>Sync</button>`) {
		t.Fatalf("synced local file should not show Sync:\n%s", html)
	}
}

func TestRootRouteRendersTreeWithoutTreeHeading(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &uiServer{cfg: Config{
		LocalRoot:       localRoot,
		RemoteRoot:      "/",
		StatePath:       filepath.Join(t.TempDir(), "state.json"),
		RemoteCachePath: filepath.Join(t.TempDir(), "remote-cache.json"),
	}, tokens: map[string]confirmToken{}, tmpl: tmpl}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	html := rec.Body.String()
	for _, want := range []string{
		`<main id="main">`,
		`class="finder"`,
		`<code>a.txt</code>`,
		`<a class="active" href="/">Tree</a>`,
		`<div class="label">conflicts</div>`,
		`name="conflicts" value="1"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("root HTML missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `<h1>Tree</h1>`) {
		t.Fatalf("tree view should not render Tree as a page heading:\n%s", html)
	}
	for _, unwanted := range []string{`href="/dashboard"`, `href="/plan"`, `href="/conflicts"`, `<select name="status"`, `Uploads `, `<div class="label">tracked deletes</div>`, `<div class="label">ignored remote-only</div>`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("root nav should not include %q:\n%s", unwanted, html)
		}
	}
	for _, unwanted := range []string{`>Search</button>`, `href="/tree">All</a>`, `placeholder="Filter by name or path"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("root tree controls should not include %q:\n%s", unwanted, html)
		}
	}
}

func TestTreeConflictFileShowsResolutionActions(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{Nodes: []UITreeNode{{
		Path:         "conflict.txt",
		Name:         "conflict.txt",
		NodeID:       "node-conflict-txt",
		Status:       "conflict",
		LocalExists:  true,
		RemoteExists: true,
		LocalSize:    5,
		RemoteSize:   5,
	}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "treeRows", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`>Keep local</button>`,
		`hx-post="/actions/path/upload?path=conflict.txt&amp;return=tree-row"`,
		`>Keep remote</button>`,
		`hx-post="/actions/path/download?path=conflict.txt&amp;return=tree-row"`,
		`>Mark synced</button>`,
		`hx-target="#entry-node-conflict-txt"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("conflict row HTML missing %q:\n%s", want, html)
		}
	}
}

func TestUIDeleteRoutesAreRegistered(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	s := &uiServer{tokens: map[string]confirmToken{}, tmpl: tmpl}
	for _, route := range []string{"/actions/path/delete-local", "/actions/path/delete-remote"} {
		req := httptest.NewRequest(http.MethodPost, route, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s was not registered", route)
		}
	}
}

func TestTreeRowPathActionMissingPathDoesNotRenderFullPage(t *testing.T) {
	s := &uiServer{tokens: map[string]confirmToken{}}
	req := httptest.NewRequest(http.MethodPost, "/actions/path/trash-local?return=tree-row", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	html := rec.Body.String()
	if !strings.Contains(html, "path is required") {
		t.Fatalf("response missing error: %s", html)
	}
	if strings.Contains(html, `<main id="main">`) || strings.Contains(html, `{{define "tree"}}`) {
		t.Fatalf("row error must not render a full page:\n%s", html)
	}
}

func TestTreeRowConfirmMultipartPathIsParsed(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	longPath := "MDYG_gesItY0qu12zfP5hXntaLheldIboUI-SC1te82N28f2932Hqs0M_-4n1gJEY-zd87lz5NZShftnbCYA95Di.jpg"
	if err := writer.WriteField("path", longPath); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("return", "tree-row"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("confirm_token", "bad-token"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	s := &uiServer{tokens: map[string]confirmToken{}}
	req := httptest.NewRequest(http.MethodPost, "/actions/path/trash-local", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	html := rec.Body.String()
	if strings.Contains(html, "path is required") {
		t.Fatalf("multipart path was not parsed:\n%s", html)
	}
	if !strings.Contains(html, "invalid or expired confirmation token") {
		t.Fatalf("response missing token error after parsing path:\n%s", html)
	}
}

func TestConfirmRequiresPath(t *testing.T) {
	s := &uiServer{tokens: map[string]confirmToken{}}
	req := httptest.NewRequest(http.MethodGet, "/partials/confirm?action=trash-local&return=tree-row", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "path is required") {
		t.Fatalf("response missing path error: %s", rec.Body.String())
	}
}

func TestAutoExecutablePlanSkipsConflictsAndNonMutatingItems(t *testing.T) {
	plan := []PlanItem{
		{Action: ActionUpload, Path: "upload.txt"},
		{Action: ActionDownload, Path: "download.txt"},
		{Action: ActionConflict, Path: "conflict.txt"},
		{Action: ActionIgnore, Path: "ignored.txt"},
		{Action: ActionMarkSynced, Path: "same.txt"},
		{Action: ActionTrashRemote, Path: "deleted-local.txt"},
	}
	got := autoExecutablePlan(plan)
	if len(got) != 3 {
		t.Fatalf("auto executable plan length = %d, want 3: %#v", len(got), got)
	}
	want := []Action{ActionUpload, ActionDownload, ActionTrashRemote}
	for i, action := range want {
		if got[i].Action != action {
			t.Fatalf("auto action %d = %s, want %s", i, got[i].Action, action)
		}
	}
}

func TestAutoSyncRemoteRefreshFallbackIsSkippedNotFatal(t *testing.T) {
	if !canDisplayCachedRemoteAfterRefreshError(uiSnapshot{HasRemote: true}, "429 Too Many Requests; using cached remote tree") {
		t.Fatal("cached remote fallback should allow auto-sync to skip without fatal error")
	}
	if canDisplayCachedRemoteAfterRefreshError(uiSnapshot{HasRemote: false}, "429 Too Many Requests") {
		t.Fatal("missing remote data should remain a fatal auto-sync error")
	}
	if canDisplayCachedRemoteAfterRefreshError(uiSnapshot{HasRemote: true}, "") {
		t.Fatal("empty error should not be treated as fallback warning")
	}
}

func TestDeletedLocalTrackedRowKeepsDeletePath(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{Nodes: []UITreeNode{{
		Path:         "gone.txt",
		Name:         "gone.txt",
		NodeID:       "node-gone-txt",
		Status:       "trash-remote",
		RemoteExists: true,
		Tracked:      true,
	}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "treeRows", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `hx-get="/partials/confirm?action=trash-remote&path=gone.txt&return=tree-row"`) {
		t.Fatalf("deleted local row should keep path in delete confirmation:\n%s", html)
	}
}

func TestRemovedLegacyUIRoutesReturnNotFound(t *testing.T) {
	tmpl, err := template.New("ui").Funcs(template.FuncMap{
		"statusLabel":  statusLabel,
		"statusIcon":   statusIcon,
		"formatSize":   formatSize,
		"formatTime":   formatTime,
		"actionClass":  actionClass,
		"canMark":      canMarkSynced,
		"syncAction":   treeSyncAction,
		"deleteAction": treeDeleteAction,
		"conflictFile": treeConflictFile,
		"query":        url.QueryEscape,
		"actionPath":   actionPathQuery,
	}).Parse(uiTemplates)
	if err != nil {
		t.Fatal(err)
	}
	s := &uiServer{tokens: map[string]confirmToken{}, tmpl: tmpl}
	for _, route := range []string{"/dashboard", "/plan", "/conflicts", "/actions/run-plan"} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		if strings.HasPrefix(route, "/actions/") {
			req = httptest.NewRequest(http.MethodPost, route, nil)
		}
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", route, rec.Code, http.StatusNotFound)
		}
	}
}

func TestFinderRowsAreRealClickableGridRows(t *testing.T) {
	if strings.Contains(uiTemplates, ".finder-head, .finder-row { display:contents; }") {
		t.Fatal("finder rows must be real boxes so folder row clicks are reliable")
	}
	if !strings.Contains(uiTemplates, ".finder-head, .finder-row { display:grid;") {
		t.Fatal("finder rows should be grid rows")
	}
}

func TestHTMXLiteHandlesFolderRowClicks(t *testing.T) {
	for _, want := range []string{
		`event.target.closest('.finder-row.folder')`,
		`var el = folderRow || event.target.closest('[hx-get],[hx-post]')`,
		`target.dataset.loaded = '1'`,
	} {
		if !strings.Contains(htmxLiteJS, want) {
			t.Fatalf("htmxLiteJS missing %q", want)
		}
	}
}

func TestHTMXLiteDoesNotQueryEmptyIndicatorSelector(t *testing.T) {
	if strings.Contains(htmxLiteJS, `querySelector(el.getAttribute('hx-indicator') || '')`) {
		t.Fatal("empty hx-indicator selector causes folder-row clicks to throw before fetch")
	}
	if !strings.Contains(htmxLiteJS, `var indicator = indicatorSelector ? document.querySelector(indicatorSelector) : null;`) {
		t.Fatal("htmxLiteJS should guard missing hx-indicator")
	}
}

func TestHTMXLiteSupportsNoSwapResponses(t *testing.T) {
	for _, want := range []string{
		`function submitHXForm(form)`,
		`var swap = form.getAttribute('hx-swap') || 'innerHTML';`,
		`var modal = form.closest('#modal');`,
		`if (!r.ok) throw new Error('HTTP ' + r.status);`,
		`if (r.status === 204 || swap === 'none') return null;`,
		`target.outerHTML = html;`,
		`target.innerHTML = html;`,
		`if (modal) modal.innerHTML = '';`,
	} {
		if !strings.Contains(htmxLiteJS, want) {
			t.Fatalf("htmxLiteJS missing %q", want)
		}
	}
}

func TestHTMXLiteDoesNotSubmitFormsFromClickHandler(t *testing.T) {
	for _, bad := range []string{
		`submit button click`,
		`var submitButton = event.target.closest`,
		`submitHXForm(submitForm);`,
	} {
		if strings.Contains(htmxLiteJS, bad) {
			t.Fatalf("click handler should not manually submit forms; found %q", bad)
		}
	}
	if !strings.Contains(htmxLiteJS, `folderRow = null;`) {
		t.Fatal("folder row click handler must not block interactive elements inside rows; should clear folderRow instead")
	}
	if !strings.Contains(htmxLiteJS, `if (el.closest('form')) return;`) {
		t.Fatal("generic click handler must not intercept form controls")
	}
}

func TestHTMXLiteSetsPendingIconForSync(t *testing.T) {
	for _, want := range []string{
		`form.classList.contains('sync-form')`,
		`icon.innerHTML = '&#8987;'`,
		`icon.title = 'pending'`,
		`icon.className = 'finder-status-icon status-pending'`,
		`buttons.forEach(function (button) { button.disabled = true; });`,
	} {
		if !strings.Contains(htmxLiteJS, want) {
			t.Fatalf("htmxLiteJS missing %q", want)
		}
	}
}

func TestHTMXLiteSubmitsWhenSearchIsCleared(t *testing.T) {
	for _, want := range []string{
		`event.target.closest('.finder-search input[type="search"]')`,
		`if (!input || input.value !== '') return;`,
		`form.requestSubmit()`,
	} {
		if !strings.Contains(htmxLiteJS, want) {
			t.Fatalf("htmxLiteJS missing %q", want)
		}
	}
}

func TestConfirmCancelDoesNotSubmit(t *testing.T) {
	if !strings.Contains(uiTemplates, `type="button" data-no-ajax onclick="event.preventDefault(); document.getElementById('modal').innerHTML=''`) {
		t.Fatal("confirmation cancel must be a non-submit button that only closes the modal")
	}
	if !strings.Contains(uiTemplates, `class="modal-actions"`) {
		t.Fatal("confirmation buttons should share modal-actions layout")
	}
	if !strings.Contains(htmxLiteJS, `if (event.target.closest('[data-no-ajax]')) return;`) {
		t.Fatal("click handler must ignore data-no-ajax controls")
	}
	formStart := strings.Index(uiTemplates, `<form hx-post="{{.Confirm.URL}}"`)
	cancel := strings.Index(uiTemplates, `data-no-ajax`)
	formEnd := strings.Index(uiTemplates[formStart:], `</form>`)
	if formStart >= 0 && cancel >= formStart && formEnd >= 0 && cancel < formStart+formEnd {
		t.Fatal("confirmation cancel must not be inside the hx-post form")
	}
}

func TestUIConfirmationTokenIsBoundAndSingleUse(t *testing.T) {
	s := &uiServer{tokens: map[string]confirmToken{}}
	token, err := s.newToken(ActionTrashRemote, "docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if s.consumeToken(ActionTrashLocal, "docs/a.txt", token) {
		t.Fatal("token matched the wrong action")
	}

	token, err = s.newToken(ActionTrashRemote, "docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !s.consumeToken(ActionTrashRemote, "docs/a.txt", token) {
		t.Fatal("token did not match its action and path")
	}
	if s.consumeToken(ActionTrashRemote, "docs/a.txt", token) {
		t.Fatal("token was reusable")
	}
}

func TestUICachedRemotesDoesNotScanWithoutCacheOnPageLoad(t *testing.T) {
	s := &uiServer{cfg: Config{
		BaseURL:         "https://webdav.invalid",
		RemoteRoot:      "/",
		User:            "user",
		Pass:            "pass",
		RemoteCachePath: filepath.Join(t.TempDir(), "missing-cache.json"),
	}}
	remotes, _, ok, msg := s.cachedRemotes(context.Background(), false)
	if ok {
		t.Fatal("expected no remote data without cache")
	}
	if len(remotes) != 0 {
		t.Fatalf("remotes length = %d, want 0", len(remotes))
	}
	if !strings.Contains(msg, "remote cache is empty") {
		t.Fatalf("message = %q, want remote cache empty", msg)
	}
}

func TestBuildTreeNodesShowsRootChildren(t *testing.T) {
	locals := map[string]LocalEntry{
		"Documents":       {Path: "Documents", IsDir: true},
		"Documents/a.txt": {Path: "Documents/a.txt", Size: 1},
		"photo.jpg":       {Path: "photo.jpg", Size: 2},
	}
	nodes := buildTreeNodes(locals, nil, SyncState{}, nil, "", "", false, false)
	if len(nodes) != 2 {
		t.Fatalf("root nodes = %d, want 2: %#v", len(nodes), nodes)
	}
	if nodes[0].Name != "Documents" || nodes[1].Name != "photo.jpg" {
		t.Fatalf("root node names = %q, %q", nodes[0].Name, nodes[1].Name)
	}
}

func TestBuildTreeNodesLocalOnlyShowsAllLocalFiles(t *testing.T) {
	locals := map[string]LocalEntry{
		"Documents/report-final.pdf": {Path: "Documents/report-final.pdf", Size: 10},
		"Documents/shared.txt":       {Path: "Documents/shared.txt", Size: 10},
	}
	remotes := map[string]RemoteEntry{
		"Documents/shared.txt":     {Path: "Documents/shared.txt", Size: 10},
		"Documents/remote-only.md": {Path: "Documents/remote-only.md", Size: 10},
	}
	nodes := buildTreeNodes(locals, remotes, SyncState{}, nil, "Documents", "", false, true)
	if len(nodes) != 2 {
		t.Fatalf("local-only nodes = %d, want 2: %#v", len(nodes), nodes)
	}
	got := []string{nodes[0].Path, nodes[1].Path}
	want := []string{"Documents/report-final.pdf", "Documents/shared.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("local-only paths = %#v, want %#v", got, want)
	}
}

func TestBuildTreeNodesSearchKeepsOnlyMatchingFoldersOrFoldersWithMatchingFiles(t *testing.T) {
	locals := map[string]LocalEntry{
		"Documents":                {Path: "Documents", IsDir: true},
		"Documents/report.pdf":     {Path: "Documents/report.pdf", Size: 10},
		"Pictures":                 {Path: "Pictures", IsDir: true},
		"Pictures/photo.jpg":       {Path: "Pictures/photo.jpg", Size: 10},
		"report-archive":           {Path: "report-archive", IsDir: true},
		"report-archive/readme.md": {Path: "report-archive/readme.md", Size: 10},
	}
	nodes := buildTreeNodes(locals, nil, SyncState{}, nil, "", "report", false, false)
	got := make([]string, 0, len(nodes))
	for _, node := range nodes {
		got = append(got, node.Path)
	}
	want := []string{"Documents", "report-archive"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("root search nodes = %#v, want %#v", got, want)
	}
	nodes = buildTreeNodes(locals, nil, SyncState{}, nil, "Documents", "report", false, false)
	if len(nodes) != 1 || nodes[0].Path != "Documents/report.pdf" {
		t.Fatalf("document search children = %#v, want report file", nodes)
	}
}

func TestBuildTreeNodesSearchDoesNotMatchChildByParentPath(t *testing.T) {
	locals := map[string]LocalEntry{
		"angelo":                     {Path: "angelo", IsDir: true},
		"angelo/Talon_Body_Snap.png": {Path: "angelo/Talon_Body_Snap.png", Size: 10},
		"angelo/angelo-notes.txt":    {Path: "angelo/angelo-notes.txt", Size: 10},
	}
	nodes := buildTreeNodes(locals, nil, SyncState{}, nil, "", "angelo", false, false)
	if len(nodes) != 1 || nodes[0].Path != "angelo" {
		t.Fatalf("root search nodes = %#v, want angelo folder", nodes)
	}
	nodes = buildTreeNodes(locals, nil, SyncState{}, nil, "angelo", "angelo", false, false)
	if len(nodes) != 1 || nodes[0].Path != "angelo/angelo-notes.txt" {
		t.Fatalf("angelo search children = %#v, want only matching child file", nodes)
	}
}

func TestBuildTreeNodesConflictToggleIncludesParentFolder(t *testing.T) {
	locals := map[string]LocalEntry{
		"Documents":              {Path: "Documents", IsDir: true},
		"Documents/conflict.txt": {Path: "Documents/conflict.txt", Size: 5},
		"clean.txt":              {Path: "clean.txt", Size: 5},
	}
	remotes := map[string]RemoteEntry{
		"Documents/conflict.txt": {Path: "Documents/conflict.txt", Size: 6},
		"clean.txt":              {Path: "clean.txt", Size: 5},
	}
	plan := []PlanItem{{Action: ActionConflict, Path: "Documents/conflict.txt"}}
	nodes := buildTreeNodes(locals, remotes, SyncState{}, plan, "", "", true, false)
	if len(nodes) != 1 || nodes[0].Path != "Documents" {
		t.Fatalf("conflict root nodes = %#v, want Documents", nodes)
	}
	nodes = buildTreeNodes(locals, remotes, SyncState{}, plan, "Documents", "", true, false)
	if len(nodes) != 1 || nodes[0].Path != "Documents/conflict.txt" {
		t.Fatalf("conflict child nodes = %#v, want conflict file", nodes)
	}
}
