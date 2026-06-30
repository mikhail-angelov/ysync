package ysync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const confirmTTL = 2 * time.Minute

type UITreeNode struct {
	Path          string
	Name          string
	NodeID        string
	Depth         int
	IsDir         bool
	Status        string
	LocalExists   bool
	RemoteExists  bool
	Tracked       bool
	LocalSize     int64
	RemoteSize    int64
	LocalModTime  time.Time
	RemoteModTime time.Time
	RemoteETag    string
	ChildrenURL   string
}

type uiServer struct {
	cfg      Config
	log      Logger
	mu       sync.Mutex
	syncMu   sync.Mutex
	remotes  map[string]RemoteEntry
	remoteAt time.Time
	ops      []operationLog
	tokens   map[string]confirmToken
	tmpl     *template.Template

	syncRunning bool
	syncStatus  string
	syncError   bool
	lastSyncAt  time.Time
}

type operationLog struct {
	At      time.Time
	Action  string
	Path    string
	Message string
	Error   bool
}

type confirmToken struct {
	Action string
	Path   string
	Expiry time.Time
}

type uiSnapshot struct {
	State       SyncState
	Locals      map[string]LocalEntry
	Remotes     map[string]RemoteEntry
	Plan        []PlanItem
	TreeNodes   []UITreeNode
	Counts      map[string]int
	RemoteAt    time.Time
	ScannedAt   time.Time
	HasRemote   bool
	RemoteError string
	SyncRunning bool
	SyncStatus  string
	SyncError   bool
	LastSyncAt  time.Time
}

type pageData struct {
	Title        string
	Active       string
	Snapshot     uiSnapshot
	Query        string
	LocalOnly    bool
	ConflictOnly bool
	Path         string
	Nodes        []UITreeNode
	Ops          []operationLog
	Settings     Config
	Message      string
	Error        string
	Confirm      *confirmData
	Full         bool
}

type confirmData struct {
	Action string
	Path   string
	Token  string
	URL    string
	Return string
	Target string
	Swap   string
}

func ServeUI(ctx context.Context, cfg Config, log Logger) error {
	s := &uiServer{
		cfg:    cfg,
		log:    log,
		tokens: map[string]confirmToken{},
	}
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
		return err
	}
	s.tmpl = tmpl
	if err := s.startAutoSync(ctx); err != nil {
		return err
	}

	addr := cfg.UIAddr
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	actual := ln.Addr().String()
	log.Info("serving UI", "url", "http://"+actual+"/")

	srv := &http.Server{Handler: s.routes()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *uiServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/tree", s.handleTree)
	mux.HandleFunc("/operations", s.handleOperations)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/partials/confirm", s.handleConfirm)
	mux.HandleFunc("/actions/refresh", s.handleRefresh)
	mux.HandleFunc("/actions/path/mark-synced", s.handlePathAction(ActionMarkSynced, false))
	mux.HandleFunc("/actions/path/upload", s.handlePathAction(ActionUpload, false))
	mux.HandleFunc("/actions/path/download", s.handlePathAction(ActionDownload, false))
	mux.HandleFunc("/actions/path/trash-remote", s.handlePathAction(ActionTrashRemote, true))
	mux.HandleFunc("/actions/path/trash-local", s.handlePathAction(ActionTrashLocal, true))
	mux.HandleFunc("/actions/path/delete-remote", s.handlePathAction(ActionDeleteRemote, true))
	mux.HandleFunc("/actions/path/delete-local", s.handlePathAction(ActionDeleteLocal, true))
	mux.HandleFunc("/actions/path/open-local", s.handleOpenLocal)
	mux.HandleFunc("/static/htmx-lite.js", s.handleScript)
	return s.logRequests(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (s *uiServer) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.Info("ui request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery, "status", status, "duration", time.Since(start))
	})
}

func (s *uiServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.renderTree(w, r, "", strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("conflicts") == "1", r.URL.Query().Get("local_only") == "1")
}

func (s *uiServer) handleTree(w http.ResponseWriter, r *http.Request) {
	parent := cleanRel(r.URL.Query().Get("path"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	conflictOnly := r.URL.Query().Get("conflicts") == "1"
	localOnly := r.URL.Query().Get("local_only") == "1"
	s.renderTree(w, r, parent, query, conflictOnly, localOnly)
}

func (s *uiServer) renderTree(w http.ResponseWriter, r *http.Request, parent, query string, conflictOnly, localOnly bool) {
	snap, msg, errMsg := s.snapshot(r.Context(), false)
	nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, parent, query, conflictOnly, localOnly)
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("rows") == "1" {
		s.renderNamed(w, "treeRows", pageData{Nodes: nodes})
		return
	}
	s.render(w, r, "tree", pageData{Title: "Tree", Active: "tree", Snapshot: snap, Nodes: nodes, Path: parent, Query: query, ConflictOnly: conflictOnly, LocalOnly: localOnly, Message: msg, Error: errMsg})
}

func (s *uiServer) renderTreeRow(w http.ResponseWriter, r *http.Request, rel, errMsg string) {
	snap, _, snapshotErr := s.snapshot(r.Context(), false)
	if snapshotErr != "" {
		if errMsg != "" {
			errMsg += "; " + snapshotErr
		} else {
			errMsg = snapshotErr
		}
	}
	s.renderTreeRowFromSnapshot(w, rel, snap, errMsg)
}

func (s *uiServer) renderTreeRowFromSnapshot(w http.ResponseWriter, rel string, snap uiSnapshot, errMsg string) {
	node, ok := treeNodeForPath(rel, snap.Locals, snap.Remotes, snap.State, snap.Plan)
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	if errMsg != "" {
		s.renderNamed(w, "treeRows", pageData{Nodes: []UITreeNode{node}, Snapshot: snap, Error: errMsg})
		return
	}
	s.renderNamed(w, "treeRows", pageData{Nodes: []UITreeNode{node}, Snapshot: snap})
}

func (s *uiServer) handleOperations(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ops := append([]operationLog(nil), s.ops...)
	s.mu.Unlock()
	s.render(w, r, "operations", pageData{Title: "Operations", Active: "operations", Ops: ops})
}

func (s *uiServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings", pageData{Title: "Settings", Active: "settings", Settings: s.cfg})
}

func (s *uiServer) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	snap, msg, errMsg := s.snapshot(r.Context(), true)
	if errMsg == "" {
		msg = "Refresh complete"
	}
	s.addOp("refresh", "", firstNonEmpty(errMsg, msg), errMsg != "")
	if firstNonEmpty(r.FormValue("return"), r.URL.Query().Get("return")) == "tree" {
		nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, "", "", false, false)
		s.render(w, r, "tree", pageData{Title: "Tree", Active: "tree", Snapshot: snap, Nodes: nodes, Message: msg, Error: errMsg})
		return
	}
	nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, "", "", false, false)
	s.render(w, r, "tree", pageData{Title: "Tree", Active: "tree", Snapshot: snap, Nodes: nodes, Message: msg, Error: errMsg})
}

func (s *uiServer) handlePathAction(action Action, dangerous bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := parseRequestForm(r); err != nil {
			s.log.Error("ui path action parse form failed", "action", action, "error", err)
			s.renderError(w, r, err)
			return
		}
		rel := cleanRel(firstNonEmpty(r.Form.Get("path"), r.URL.Query().Get("path")))
		returnTo := firstNonEmpty(r.Form.Get("return"), r.URL.Query().Get("return"))
		s.log.Info("ui path action requested", "action", action, "path", rel, "form_path", r.Form.Get("path"), "query_path", r.URL.Query().Get("path"), "return", returnTo, "dangerous", dangerous, "has_confirm_token", r.Form.Get("confirm_token") != "")
		if rel == "" {
			if returnTo == "tree-row" {
				s.renderTreeRowActionError(w, fmt.Errorf("path is required"))
				return
			}
			s.renderError(w, r, fmt.Errorf("path is required"))
			return
		}
		if dangerous && !s.consumeToken(action, rel, r.Form.Get("confirm_token")) {
			s.log.Warn("ui path action rejected confirm token", "action", action, "path", rel)
			if returnTo == "tree-row" {
				s.renderTreeRowActionError(w, fmt.Errorf("invalid or expired confirmation token"))
				return
			}
			s.renderError(w, r, fmt.Errorf("invalid or expired confirmation token"))
			return
		}
		err := s.runPathAction(r.Context(), action, rel)
		if err != nil {
			s.addOp(string(action), rel, err.Error(), true)
			if returnTo == "tree-row" {
				s.renderTreeRow(w, r, rel, err.Error())
				return
			}
			if returnTo == "tree" {
				snap, _, errMsg := s.snapshot(r.Context(), false)
				if errMsg != "" {
					errMsg = err.Error() + "; " + errMsg
				} else {
					errMsg = err.Error()
				}
				nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, "", "", false, false)
				s.render(w, r, "tree", pageData{Title: "Tree", Active: "tree", Snapshot: snap, Nodes: nodes, Error: errMsg})
				return
			}
			s.renderError(w, r, err)
			return
		}
		s.log.Info("ui path action completed", "action", action, "path", rel)
		s.addOp(string(action), rel, "completed", false)
		snap, _, errMsg := s.snapshot(r.Context(), false)
		if returnTo == "tree-row" {
			s.renderTreeRowFromSnapshot(w, rel, snap, errMsg)
			return
		}
		if returnTo == "tree" {
			nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, "", "", false, false)
			s.render(w, r, "tree", pageData{Title: "Tree", Active: "tree", Snapshot: snap, Nodes: nodes, Message: string(action) + " completed", Error: errMsg})
			return
		}
		nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, "", "", false, false)
		s.render(w, r, "tree", pageData{Title: "Tree", Active: "tree", Snapshot: snap, Nodes: nodes, Message: string(action) + " completed", Error: errMsg})
	}
}

func (s *uiServer) handleOpenLocal(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	rel := cleanRel(r.FormValue("path"))
	s.log.Info("ui open local requested", "path", rel)
	if rel == "" {
		s.renderError(w, r, fmt.Errorf("path is required"))
		return
	}
	full := filepath.Join(s.cfg.LocalRoot, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil {
		s.log.Error("ui open local stat failed", "path", rel, "full", full, "error", err)
		s.renderError(w, r, err)
		return
	}
	args := []string{"-R", full}
	if info.IsDir() {
		args = []string{full}
	}
	s.log.Info("ui open local exec", "path", rel, "command", "open", "args", strings.Join(args, " "))
	if err := exec.Command("open", args...).Run(); err != nil {
		s.log.Error("ui open local exec failed", "path", rel, "error", err)
		s.renderError(w, r, err)
		return
	}
	s.log.Info("ui open local completed", "path", rel)
	s.addOp("open-local", rel, "opened in Finder", false)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

func (s *uiServer) handleConfirm(w http.ResponseWriter, r *http.Request) {
	action := Action(r.URL.Query().Get("action"))
	rel := cleanRel(r.URL.Query().Get("path"))
	returnTo := r.URL.Query().Get("return")
	s.log.Info("ui confirm requested", "action", action, "path", rel)
	if action == "" {
		http.Error(w, "action is required", http.StatusBadRequest)
		return
	}
	if rel == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	token, err := s.newToken(action, rel)
	if err != nil {
		s.log.Error("ui confirm token failed", "action", action, "path", rel, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("ui confirm token created", "action", action, "path", rel)
	url := "/actions/path/" + string(action)
	target := "#main"
	swap := ""
	if returnTo == "tree-row" {
		target = "#entry-" + treeNodeID(rel)
		swap = "outerHTML"
	}
	s.renderNamed(w, "confirm", pageData{Confirm: &confirmData{Action: string(action), Path: rel, Token: token, URL: url, Return: returnTo, Target: target, Swap: swap}})
}

func (s *uiServer) renderTreeRowActionError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<div class="finder-child-slot finder-empty"><span class="status-error">! %s</span></div>`, template.HTMLEscapeString(err.Error()))
}

func (s *uiServer) handleScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, htmxLiteJS)
}

func (s *uiServer) startAutoSync(ctx context.Context) error {
	ignores, err := NewIgnoreMatcher(s.cfg.IgnoreFile, s.cfg.IncludeHidden)
	if err != nil {
		return err
	}
	changes := make(chan string, 128)
	watcher, err := StartLocalWatcher(ctx, s.cfg.LocalRoot, ignores, s.cfg.Debounce, s.cfg.LocalScanInterval, s.log, func(rel string) {
		s.log.Info("auto sync queued", "path", rel)
		s.addOp("fs", rel, "stable local change queued", false)
		select {
		case changes <- rel:
		default:
			s.log.Warn("auto sync queue full; waiting to enqueue", "path", rel)
			go func() {
				select {
				case changes <- rel:
				case <-ctx.Done():
				}
			}()
		}
	})
	if err != nil {
		return err
	}
	s.log.Info("auto sync watcher started", "mode", watcher.Mode())
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case rel := <-changes:
				s.log.Info("auto sync started", "path", rel)
				s.addOp("auto-sync", rel, "started", false)
				if err := s.runAutoSync(ctx, rel); err != nil {
					s.log.Error("auto sync failed", "path", rel, "error", err)
					s.addOp("auto-sync", rel, err.Error(), true)
					s.setSyncStatus(false, err.Error(), true, false)
				} else {
					s.log.Info("auto sync finished", "path", rel)
				}
			}
		}
	}()
	select {
	case changes <- "startup":
	default:
	}
	return nil
}

func (s *uiServer) runAutoSync(ctx context.Context, rel string) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	s.setSyncStatus(true, "Syncing "+displaySyncPath(rel), false, false)
	snap, errMsg := s.autoSyncSnapshot(ctx)
	if errMsg != "" {
		if canDisplayCachedRemoteAfterRefreshError(snap, errMsg) {
			msg := "skipped: remote refresh failed; " + errMsg
			s.log.Warn("auto sync skipped", "path", rel, "error", errMsg)
			s.addOp("auto-sync", rel, msg, false)
			s.setSyncStatus(false, "Skipped: "+errMsg, false, false)
			return nil
		}
		return fmt.Errorf("%s", errMsg)
	}
	plan := autoExecutablePlan(snap.Plan)
	if len(plan) == 0 {
		s.log.Info("auto sync found no executable changes", "path", rel, "plan_items", len(snap.Plan))
		s.addOp("auto-sync", rel, fmt.Sprintf("no executable changes (%d plan items)", len(snap.Plan)), false)
		s.setSyncStatus(false, "Up to date", false, false)
		return nil
	}
	s.log.Info("auto sync plan ready", "path", rel, "operations", len(plan))
	s.addOp("auto-sync", rel, fmt.Sprintf("plan ready: %d operations", len(plan)), false)
	for _, item := range plan {
		s.log.Info("auto sync plan item", "trigger", rel, "action", item.Action, "path", item.Path, "reason", item.Reason)
	}
	client, err := s.client()
	if err != nil {
		return err
	}
	next, err := ExecutePlan(ctx, s.cfg, client, snap.State, plan, s.log)
	if err == nil {
		err = SaveStateAtomic(s.cfg.StatePath, next)
	}
	if err != nil {
		return err
	}
	s.applyPlanToRemoteCache(next, plan)
	msg := fmt.Sprintf("executed %d operations", len(plan))
	s.addOp("auto-sync", rel, msg, false)
	s.setSyncStatus(false, msg, false, true)
	return nil
}

func (s *uiServer) autoSyncSnapshot(ctx context.Context) (uiSnapshot, string) {
	snap, _, errMsg := s.snapshot(ctx, false)
	if errMsg == "" {
		return snap, ""
	}
	if snap.HasRemote || !strings.Contains(errMsg, "remote cache is empty") {
		return snap, errMsg
	}
	refreshed, _, refreshErr := s.snapshot(ctx, true)
	return refreshed, refreshErr
}

func canDisplayCachedRemoteAfterRefreshError(snap uiSnapshot, errMsg string) bool {
	return errMsg != "" && snap.HasRemote
}

func (s *uiServer) applyPlanToRemoteCache(state SyncState, plan []PlanItem) {
	s.mu.Lock()
	if s.remotes == nil {
		s.remotes = map[string]RemoteEntry{}
	}
	changed := false
	for _, item := range plan {
		rel := cleanRel(item.Path)
		switch item.Action {
		case ActionUpload, ActionDownload:
			if entry, ok := state.Entries[rel]; ok {
				s.remotes[rel] = RemoteEntry{
					Path:    rel,
					IsDir:   entry.IsDir,
					ModTime: entry.RemoteModTime,
					Size:    entry.RemoteSize,
					ETag:    entry.RemoteETag,
				}
				changed = true
			}
		case ActionTrashRemote, ActionDeleteRemote:
			deleteRemoteCacheSubtree(s.remotes, rel)
			changed = true
		}
	}
	if s.remoteAt.IsZero() {
		s.remoteAt = time.Now().UTC()
	}
	remotes := cloneRemotes(s.remotes)
	remoteAt := s.remoteAt
	s.mu.Unlock()
	if !changed {
		return
	}
	if err := SaveRemoteTreeCache(s.cfg.RemoteCachePath, s.cfg, remoteAt, remotes); err != nil {
		s.log.Warn("failed to persist remote cache update", "error", err)
	}
}

func deleteRemoteCacheSubtree(remotes map[string]RemoteEntry, rel string) {
	delete(remotes, rel)
	prefix := rel + "/"
	for p := range remotes {
		if strings.HasPrefix(p, prefix) {
			delete(remotes, p)
		}
	}
}

func displaySyncPath(rel string) string {
	if rel == "" || rel == "startup" {
		return "changes"
	}
	return rel
}

func (s *uiServer) setSyncStatus(running bool, msg string, isErr bool, markSynced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncRunning = running
	s.syncStatus = msg
	s.syncError = isErr
	if markSynced {
		s.lastSyncAt = time.Now().UTC()
	}
}

func (s *uiServer) syncStatusSnapshot(state SyncState) (bool, string, bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := s.syncStatus
	if msg == "" {
		msg = "Watching"
	}
	last := s.lastSyncAt
	if last.IsZero() {
		last = state.LastSyncedAt
	}
	return s.syncRunning, msg, s.syncError, last
}

func (s *uiServer) snapshot(ctx context.Context, forceRemote bool) (uiSnapshot, string, string) {
	scannedAt := time.Now().UTC()
	ignores, err := NewIgnoreMatcher(s.cfg.IgnoreFile, s.cfg.IncludeHidden)
	if err != nil {
		return uiSnapshot{ScannedAt: scannedAt}, "", err.Error()
	}
	state, err := LoadState(s.cfg.StatePath)
	if err != nil {
		return uiSnapshot{ScannedAt: scannedAt}, "", err.Error()
	}
	if err := EnsureStateRoots(state, s.cfg.LocalRoot, s.cfg.RemoteRoot); err != nil {
		return uiSnapshot{ScannedAt: scannedAt}, "", err.Error()
	}
	locals, err := ScanLocal(s.cfg.LocalRoot, ignores, s.log)
	if err != nil {
		return uiSnapshot{State: state, ScannedAt: scannedAt}, "", err.Error()
	}
	remotes, remoteAt, hasRemote, remoteErr := s.cachedRemotes(ctx, forceRemote)
	var plan []PlanItem
	if remoteErr == "" || hasRemote {
		plan = Compare(state, locals, remotes, CompareOptions{
			ImportRemote: s.cfg.ImportRemote,
			InitStrategy: s.cfg.InitStrategy,
			Delete:       s.cfg.Delete,
			DeleteMode:   s.cfg.DeleteMode,
		})
	}
	syncRunning, syncStatus, syncError, lastSyncAt := s.syncStatusSnapshot(state)
	snap := uiSnapshot{
		State:       state,
		Locals:      locals,
		Remotes:     remotes,
		Plan:        plan,
		Counts:      countPlan(plan),
		RemoteAt:    remoteAt,
		ScannedAt:   scannedAt,
		HasRemote:   hasRemote,
		RemoteError: remoteErr,
		SyncRunning: syncRunning,
		SyncStatus:  syncStatus,
		SyncError:   syncError,
		LastSyncAt:  lastSyncAt,
	}
	return snap, "", remoteErr
}

func (s *uiServer) cachedRemotes(ctx context.Context, force bool) (map[string]RemoteEntry, time.Time, bool, string) {
	client, err := s.client()
	if err != nil {
		return map[string]RemoteEntry{}, time.Time{}, false, err.Error()
	}
	s.mu.Lock()
	if !force && !s.remoteAt.IsZero() {
		remotes := cloneRemotes(s.remotes)
		at := s.remoteAt
		s.mu.Unlock()
		return remotes, at, true, ""
	}
	s.mu.Unlock()

	var fallback RemoteTreeCache
	hasFallback := false
	cache, ok, cacheErr := LoadRemoteTreeCache(s.cfg.RemoteCachePath, s.cfg)
	if cacheErr != nil {
		s.log.Warn("failed to load remote cache", "error", cacheErr)
	}
	if ok {
		fallback = cache
		hasFallback = true
		if !force {
			s.mu.Lock()
			s.remotes = cloneRemotes(cache.Entries)
			s.remoteAt = cache.ScannedAt
			s.mu.Unlock()
			return cloneRemotes(cache.Entries), cache.ScannedAt, true, ""
		}
	}
	if !force {
		return map[string]RemoteEntry{}, time.Time{}, false, "remote cache is empty; use Refresh to scan remote metadata"
	}

	remotes, err := LoadRemoteTree(ctx, client, s.cfg.RemoteWorkers, s.log)
	if err != nil {
		if hasFallback {
			s.mu.Lock()
			s.remotes = cloneRemotes(fallback.Entries)
			s.remoteAt = fallback.ScannedAt
			s.mu.Unlock()
			return cloneRemotes(fallback.Entries), fallback.ScannedAt, true, fmt.Sprintf("%s; using cached remote tree from %s", err, formatTime(fallback.ScannedAt))
		}
		return map[string]RemoteEntry{}, time.Time{}, false, err.Error()
	}
	now := time.Now().UTC()
	if err := SaveRemoteTreeCache(s.cfg.RemoteCachePath, s.cfg, now, remotes); err != nil {
		s.log.Warn("failed to save remote cache", "error", err)
	}
	s.mu.Lock()
	s.remotes = cloneRemotes(remotes)
	s.remoteAt = now
	s.mu.Unlock()
	return remotes, now, true, ""
}

func (s *uiServer) client() (*WebDAVClient, error) {
	if s.cfg.User == "" && s.cfg.Pass == "" {
		return nil, fmt.Errorf("remote credentials are required")
	}
	client, err := NewWebDAVClient(s.cfg.BaseURL, s.cfg.RemoteRoot, s.cfg.User, s.cfg.Pass, nil)
	if err != nil {
		return nil, err
	}
	client.Log = s.log
	return client, nil
}

func (s *uiServer) runPathAction(ctx context.Context, action Action, rel string) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	snap, _, errMsg := s.snapshot(ctx, false)
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if action == ActionMarkSynced {
		return s.markSynced(rel, snap)
	}
	client, err := s.client()
	if err != nil {
		return err
	}
	// For local delete/trash, also perform the corresponding remote action
	// atomically under the same lock. Relying on the auto-sync cycle to
	// propagate the deletion is fragile: remote cache metadata drift can
	// cause remoteDiffers to return true, turning the deletion into a
	// conflict (localDeleted && remoteChanged).
	plan := []PlanItem{{Action: action, Path: rel, Reason: "user requested from UI"}}
	switch action {
	case ActionTrashLocal:
		plan = append(plan, PlanItem{Action: ActionTrashRemote, Path: rel, Reason: "paired remote trash for local delete"})
	case ActionDeleteLocal:
		plan = append(plan, PlanItem{Action: ActionDeleteRemote, Path: rel, Reason: "paired remote delete for local delete"})
	}
	next, err := ExecutePlan(ctx, s.cfg, client, snap.State, plan, s.log)
	if err == nil {
		err = SaveStateAtomic(s.cfg.StatePath, next)
	}
	if err == nil {
		s.applyPlanToRemoteCache(next, plan)
		s.setSyncStatus(false, string(action)+" completed", false, action == ActionUpload || action == ActionDownload)
	}
	return err
}

func (s *uiServer) markSynced(rel string, snap uiSnapshot) error {
	local, hasLocal := snap.Locals[rel]
	remote, hasRemote := snap.Remotes[rel]
	if !hasLocal || !hasRemote || !canMarkSynced(nodeFrom(rel, local, hasLocal, remote, hasRemote, snap.State)) {
		return fmt.Errorf("mark synced is not safe for %s", rel)
	}
	state := snap.State
	if state.Entries == nil {
		state.Entries = map[string]SyncEntry{}
	}
	state.LocalRoot = s.cfg.LocalRoot
	state.RemoteRoot = s.cfg.RemoteRoot
	state.Entries[rel] = EntryFromMetadata(rel, local, remote, StatusSynced)
	return SaveStateAtomic(s.cfg.StatePath, state)
}

func (s *uiServer) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	data.Full = r.Header.Get("HX-Request") != "true"
	if data.Full {
		s.renderNamed(w, "layout", data)
		return
	}
	s.renderNamed(w, name, data)
}

func (s *uiServer) renderNamed(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *uiServer) renderError(w http.ResponseWriter, r *http.Request, err error) {
	snap, _, errMsg := s.snapshot(r.Context(), false)
	msg := err.Error()
	if errMsg != "" {
		msg += "; " + errMsg
	}
	nodes := buildTreeNodes(snap.Locals, snap.Remotes, snap.State, snap.Plan, "", "", false, false)
	s.render(w, r, "tree", pageData{Title: "Error", Active: "tree", Snapshot: snap, Nodes: nodes, Error: msg})
}

func (s *uiServer) newToken(action Action, rel string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = confirmToken{Action: string(action), Path: rel, Expiry: time.Now().Add(confirmTTL)}
	return token, nil
}

func (s *uiServer) consumeToken(action Action, rel, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ct, ok := s.tokens[token]
	if !ok {
		return false
	}
	delete(s.tokens, token)
	return ct.Action == string(action) && ct.Path == rel && time.Now().Before(ct.Expiry)
}

func (s *uiServer) addOp(action, rel, msg string, isErr bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append([]operationLog{{At: time.Now().UTC(), Action: action, Path: rel, Message: msg, Error: isErr}}, s.ops...)
	if len(s.ops) > 100 {
		s.ops = s.ops[:100]
	}
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func parseRequestForm(r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return r.ParseMultipartForm(1 << 20)
	}
	return r.ParseForm()
}

func autoExecutablePlan(plan []PlanItem) []PlanItem {
	out := make([]PlanItem, 0, len(plan))
	for _, item := range plan {
		switch item.Action {
		case ActionUpload, ActionDownload, ActionTrashRemote, ActionTrashLocal, ActionDeleteRemote, ActionDeleteLocal:
			out = append(out, item)
		}
	}
	return out
}

func countPlan(plan []PlanItem) map[string]int {
	counts := map[string]int{"upload": 0, "download": 0, "conflict": 0, "delete": 0, "ignored": 0, "mark-synced": 0, "error": 0}
	for _, item := range plan {
		switch item.Action {
		case ActionUpload:
			counts["upload"]++
		case ActionDownload:
			counts["download"]++
		case ActionConflict:
			counts["conflict"]++
		case ActionTrashRemote, ActionTrashLocal, ActionDeleteRemote, ActionDeleteLocal:
			counts["delete"]++
		case ActionIgnore:
			counts["ignored"]++
		case ActionMarkSynced:
			counts["mark-synced"]++
		}
	}
	return counts
}

func buildTreeNodes(locals map[string]LocalEntry, remotes map[string]RemoteEntry, state SyncState, plan []PlanItem, parent, query string, conflictOnly, localOnly bool) []UITreeNode {
	statuses := map[string]string{}
	for _, item := range plan {
		statuses[item.Path] = uiStatus(item.Action)
	}
	paths := map[string]struct{}{}
	for p := range locals {
		paths[p] = struct{}{}
	}
	for p := range remotes {
		paths[p] = struct{}{}
	}
	for p := range state.Entries {
		paths[p] = struct{}{}
	}
	var nodes []UITreeNode
	for p := range paths {
		if parentPath(p) != parent {
			continue
		}
		local, hasLocal := locals[p]
		remote, hasRemote := remotes[p]
		node := nodeFrom(p, local, hasLocal, remote, hasRemote, state)
		if status, ok := statuses[p]; ok {
			node.Status = status
		}
		if !treeNodeVisible(p, node, paths, locals, remotes, state, statuses, query, conflictOnly, localOnly) {
			continue
		}
		node.ChildrenURL = treeChildrenURL(p, query, conflictOnly, localOnly)
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return nodes[i].Path < nodes[j].Path
	})
	return nodes
}

func treeNodeVisible(rel string, node UITreeNode, paths map[string]struct{}, locals map[string]LocalEntry, remotes map[string]RemoteEntry, state SyncState, statuses map[string]string, query string, conflictOnly, localOnly bool) bool {
	if treeNodeDirectlyMatches(rel, node, query, conflictOnly, localOnly) {
		return true
	}
	return node.IsDir && treeDirHasMatchingFile(rel, paths, locals, remotes, state, statuses, query, conflictOnly, localOnly)
}

func treeNodeDirectlyMatches(rel string, node UITreeNode, query string, conflictOnly, localOnly bool) bool {
	if localOnly && !node.LocalExists {
		return false
	}
	if conflictOnly && statusLabel(node.Status) != "conflict" {
		return false
	}
	if query != "" && !fuzzyMatchTreePath(rel, query) {
		return false
	}
	return true
}

func treeDirHasMatchingFile(dir string, paths map[string]struct{}, locals map[string]LocalEntry, remotes map[string]RemoteEntry, state SyncState, statuses map[string]string, query string, conflictOnly, localOnly bool) bool {
	prefix := cleanRel(dir) + "/"
	for p := range paths {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		local, hasLocal := locals[p]
		remote, hasRemote := remotes[p]
		node := nodeFrom(p, local, hasLocal, remote, hasRemote, state)
		if node.IsDir {
			continue
		}
		if status, ok := statuses[p]; ok {
			node.Status = status
		}
		if treeNodeDirectlyMatches(p, node, query, conflictOnly, localOnly) {
			return true
		}
	}
	return false
}

func treeNodeForPath(rel string, locals map[string]LocalEntry, remotes map[string]RemoteEntry, state SyncState, plan []PlanItem) (UITreeNode, bool) {
	rel = cleanRel(rel)
	local, hasLocal := locals[rel]
	remote, hasRemote := remotes[rel]
	_, tracked := state.Entries[rel]
	if !hasLocal && !hasRemote && !tracked {
		return UITreeNode{}, false
	}
	node := nodeFrom(rel, local, hasLocal, remote, hasRemote, state)
	for _, item := range plan {
		if item.Path == rel {
			node.Status = uiStatus(item.Action)
			break
		}
	}
	return node, true
}

func nodeFrom(rel string, local LocalEntry, hasLocal bool, remote RemoteEntry, hasRemote bool, state SyncState) UITreeNode {
	prev, tracked := state.Entries[rel]
	name := path.Base(rel)
	if rel == "" || name == "." {
		name = "/"
	}
	status := "unknown"
	if tracked {
		status = prev.Status
		if status == "" || status == StatusSynced {
			status = "synced"
		}
	} else if hasLocal && !hasRemote {
		status = "local-only"
	} else if !hasLocal && hasRemote {
		status = "remote-only-ignored"
	} else if hasLocal && hasRemote {
		status = "unknown"
	}
	isDir := local.IsDir || remote.IsDir || prev.IsDir
	return UITreeNode{
		Path:          rel,
		Name:          name,
		NodeID:        treeNodeID(rel),
		Depth:         treeDepth(rel),
		IsDir:         isDir,
		Status:        status,
		LocalExists:   hasLocal,
		RemoteExists:  hasRemote,
		Tracked:       tracked,
		LocalSize:     local.Size,
		RemoteSize:    remote.Size,
		LocalModTime:  local.ModTime,
		RemoteModTime: remote.ModTime,
		RemoteETag:    remote.ETag,
		ChildrenURL:   treeChildrenURL(rel, "", false, false),
	}
}

func parentPath(rel string) string {
	rel = cleanRel(rel)
	if rel == "" || !strings.Contains(rel, "/") {
		return ""
	}
	return path.Dir(rel)
}

func treeDepth(rel string) int {
	rel = cleanRel(rel)
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/")
}

func treeNodeID(rel string) string {
	if rel == "" {
		return "root"
	}
	var b strings.Builder
	b.WriteString("node-")
	for _, r := range rel {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func treeChildrenURL(rel, query string, conflictOnly, localOnly bool) string {
	values := url.Values{}
	values.Set("rows", "1")
	values.Set("path", rel)
	if query != "" {
		values.Set("q", query)
	}
	if conflictOnly {
		values.Set("conflicts", "1")
	}
	if localOnly {
		values.Set("local_only", "1")
	}
	return "/tree?" + values.Encode()
}

func actionPathQuery(rel, returnTo string) string {
	values := url.Values{}
	values.Set("path", rel)
	if returnTo != "" {
		values.Set("return", returnTo)
	}
	return values.Encode()
}

func fuzzyMatchTreePath(rel, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	name := strings.ToLower(path.Base(rel))
	return fuzzyContains(name, query)
}

func fuzzyContains(value, query string) bool {
	if strings.Contains(value, query) {
		return true
	}
	valueRunes := []rune(value)
	queryRunes := []rune(query)
	j := 0
	for _, r := range valueRunes {
		if j < len(queryRunes) && r == queryRunes[j] {
			j++
		}
	}
	return j == len(queryRunes)
}

func canMarkSynced(node UITreeNode) bool {
	return node.LocalExists && node.RemoteExists && node.LocalSize == node.RemoteSize
}

func treeSyncAction(node UITreeNode) string {
	if !node.IsDir && node.RemoteExists && !node.LocalExists {
		return string(ActionDownload)
	}
	return ""
}

func treeConflictFile(node UITreeNode) bool {
	return !node.IsDir && statusLabel(node.Status) == "conflict"
}

func treeDeleteAction(node UITreeNode) string {
	switch {
	case node.LocalExists && node.RemoteExists:
		return string(ActionTrashLocal)
	case node.LocalExists:
		return string(ActionTrashLocal)
	case node.RemoteExists:
		if node.IsDir {
			return string(ActionDeleteRemote)
		}
		return string(ActionTrashRemote)
	default:
		return ""
	}
}

func uiStatus(action Action) string {
	switch action {
	case ActionUpload:
		return "upload"
	case ActionDownload:
		return "download"
	case ActionConflict:
		return "conflict"
	case ActionTrashRemote:
		return "trash-remote"
	case ActionTrashLocal:
		return "trash-local"
	case ActionDeleteRemote:
		return "delete-remote"
	case ActionDeleteLocal:
		return "delete-local"
	case ActionIgnore:
		return "remote-only-ignored"
	case ActionMarkSynced:
		return "mark-synced"
	default:
		return "synced"
	}
}

func statusLabel(status string) string {
	switch status {
	case StatusPendingUpload:
		return "upload"
	case StatusPendingDownload:
		return "download"
	case StatusConflict:
		return "conflict"
	case StatusError:
		return "error"
	default:
		return strings.ReplaceAll(status, "_", "-")
	}
}

func statusIcon(status string) template.HTML {
	switch statusLabel(status) {
	case "synced", "mark-synced":
		return "&#10003;"
	case "upload", "local-only":
		return "&uarr;"
	case "download":
		return "&darr;"
	case "conflict":
		return "&#9888;"
	case "trash-remote", "trash-local", "delete-remote", "delete-local", "deleted-local", "deleted-remote":
		return "&#128465;"
	case "remote-only-ignored", "ignored":
		return "&#128065;&#65038;"
	case "error":
		return "!"
	default:
		return "?"
	}
}

func actionClass(action string) string {
	return "action-" + strings.ReplaceAll(action, "_", "-")
}

func formatSize(size int64) string {
	if size == 0 {
		return "-"
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func cloneRemotes(in map[string]RemoteEntry) map[string]RemoteEntry {
	out := make(map[string]RemoteEntry, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

const htmxLiteJS = `
document.addEventListener('click', function (event) {
  if (event.target.closest('[data-no-ajax]')) return;
  var folderRow = event.target.closest('.finder-row.folder');
  if (folderRow && event.target.closest('a,button,form,input,select,textarea,label')) {
    folderRow = null;
  }
  var el = folderRow || event.target.closest('[hx-get],[hx-post]');
  if (!el) return;
  if (el.closest('form')) return;
  var targetSelector = el.getAttribute('hx-target') || '#main';
  var swap = el.getAttribute('hx-swap') || 'innerHTML';
  var target = document.querySelector(targetSelector);
  if (folderRow && target && target.dataset.loaded === '1') {
    event.preventDefault();
    var collapsed = target.style.display === 'none';
    console.debug('[ysync-ui] toggle folder', el.getAttribute('hx-get'), collapsed ? 'expand' : 'collapse');
    target.style.display = collapsed ? '' : 'none';
    var chevron = el.querySelector('.finder-chevron');
    if (chevron) chevron.innerHTML = collapsed ? '&#9662;' : '&#9656;';
    return;
  }
  event.preventDefault();
  var method = el.getAttribute('hx-post') ? 'POST' : 'GET';
  var url = el.getAttribute('hx-post') || el.getAttribute('hx-get');
  console.debug('[ysync-ui] ajax click', method, url, 'target', targetSelector, 'swap', swap);
  var indicatorSelector = el.getAttribute('hx-indicator');
  var indicator = indicatorSelector ? document.querySelector(indicatorSelector) : null;
  var disabled = el.getAttribute('hx-disabled-elt') === 'this';
  var body = null;
  var headers = {'HX-Request': 'true'};
  if (method === 'POST') {
    var form = el.closest('form');
    body = form ? new FormData(form) : new FormData();
  }
  if (indicator) indicator.style.display = 'inline';
  if (disabled) el.disabled = true;
  fetch(url, {method: method, body: body, headers: headers})
    .then(function (r) { console.debug('[ysync-ui] ajax response', method, url, r.status); if (r.status === 204 || swap === 'none') return null; return r.text(); })
    .then(function (html) {
      if (html !== null && target) {
        if (swap === 'outerHTML') {
          target.outerHTML = html;
        } else {
          target.innerHTML = html;
        }
        if (folderRow) {
          target.dataset.loaded = '1';
          target.style.display = '';
          var chevron = el.querySelector('.finder-chevron');
          if (chevron) chevron.innerHTML = '&#9662;';
        }
      }
    })
    .finally(function () {
      if (indicator) indicator.style.display = '';
      if (disabled) el.disabled = false;
    })
    .catch(function (err) { console.error('[ysync-ui] ajax failed', method, url, err); });
});
document.addEventListener('submit', function (event) {
  var form = event.target.closest('form[hx-post]');
  if (!form) return;
  event.preventDefault();
  submitHXForm(form);
});
document.addEventListener('input', function (event) {
  var input = event.target.closest('.finder-search input[type="search"]');
  if (!input || input.value !== '') return;
  var form = input.form;
  if (form && typeof form.requestSubmit === 'function') form.requestSubmit();
  else if (form) form.submit();
});
function submitHXForm(form) {
  var swap = form.getAttribute('hx-swap') || 'innerHTML';
  var target = document.querySelector(form.getAttribute('hx-target') || '#main');
  var url = form.getAttribute('hx-post');
  var path = new FormData(form).get('path');
  var modal = form.closest('#modal');
  console.debug('[ysync-ui] form submit', url, 'path', path, 'swap', swap);
  var buttons = form.querySelectorAll('button');
  buttons.forEach(function (button) { button.disabled = true; });
  if (form.classList.contains('sync-form')) {
    var row = form.closest('.finder-row');
    var icon = row ? row.querySelector('.finder-status-icon') : null;
    if (icon) {
      icon.innerHTML = '&#8987;';
      icon.title = 'pending';
      icon.className = 'finder-status-icon status-pending';
    }
  }
  fetch(url, {method: 'POST', body: new FormData(form), headers: {'HX-Request': 'true'}})
    .then(function (r) { console.debug('[ysync-ui] form response', url, r.status); if (!r.ok) throw new Error('HTTP ' + r.status); if (r.status === 204 || swap === 'none') return null; return r.text(); })
    .then(function (html) {
      if (html !== null && target) {
        if (swap === 'outerHTML') {
          target.outerHTML = html;
        } else {
          target.innerHTML = html;
        }
        if (modal) modal.innerHTML = '';
      } else {
        buttons.forEach(function (button) { button.disabled = false; });
      }
    })
    .catch(function (err) {
      buttons.forEach(function (button) { button.disabled = false; });
      console.error('[ysync-ui] form failed', url, err);
    });
}
`

const uiTemplates = `
{{define "layout"}}
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ysync - {{.Title}}</title>
  <script src="/static/htmx-lite.js" defer></script>
  <style>
    :root { color-scheme: light; --bg:#f7f8fb; --panel:#fff; --line:#d9dde7; --text:#20242d; --muted:#687084; --accent:#0f766e; --bad:#b42318; --warn:#a15c00; --blue:#1d4ed8; }
    * { box-sizing: border-box; }
    body { margin:0; font:14px/1.45 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:var(--bg); color:var(--text); }
    header { display:flex; align-items:center; gap:18px; padding:14px 20px; border-bottom:1px solid var(--line); background:#ffffff; position:sticky; top:0; z-index:2; }
    .brand { font-weight:700; font-size:18px; }
    nav { display:flex; flex-wrap:wrap; gap:6px; }
    nav a { color:var(--muted); text-decoration:none; padding:7px 9px; border-radius:6px; }
    nav a.active { background:#e8f3f1; color:#0b5f58; }
    main { max-width:1180px; margin:0 auto; padding:22px; }
    .bar { display:flex; gap:10px; align-items:center; justify-content:space-between; margin-bottom:16px; flex-wrap:wrap; }
    h1 { font-size:24px; margin:0; letter-spacing:0; }
    h2 { font-size:17px; margin:22px 0 10px; letter-spacing:0; }
    .panel { background:var(--panel); border:1px solid var(--line); border-radius:8px; padding:14px; }
    .grid { display:grid; gap:12px; grid-template-columns:repeat(auto-fit,minmax(150px,1fr)); margin-bottom:16px; }
    .metric { background:#fff; border:1px solid var(--line); border-radius:8px; padding:12px; min-height:78px; }
    .metric .value { font-size:28px; font-weight:700; }
    .metric .value.small { font-size:17px; line-height:1.25; overflow-wrap:anywhere; }
    .metric .label { color:var(--muted); }
    table { width:100%; border-collapse:collapse; background:#fff; border:1px solid var(--line); border-radius:8px; overflow:hidden; }
    th, td { text-align:left; padding:9px 10px; border-bottom:1px solid var(--line); vertical-align:top; }
    th { background:#f0f2f6; color:#3d4554; font-size:12px; text-transform:uppercase; }
    tr:last-child td { border-bottom:0; }
    code { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; font-size:12px; }
    button, .button { border:1px solid var(--line); background:#fff; color:var(--text); border-radius:6px; padding:7px 10px; cursor:pointer; text-decoration:none; display:inline-flex; align-items:center; gap:6px; min-height:34px; }
    button.primary { background:var(--accent); color:white; border-color:var(--accent); }
    button.danger { background:#fff5f5; color:var(--bad); border-color:#f3b8b3; }
    button:disabled { opacity:.6; cursor:wait; }
    input, select { min-height:34px; border:1px solid var(--line); border-radius:6px; padding:6px 8px; background:#fff; }
    .filters { display:flex; gap:8px; flex-wrap:wrap; align-items:center; }
    .finder { border:1px solid var(--line); border-radius:8px; overflow:hidden; background:#fff; min-height:560px; }
    .finder-main { min-width:0; }
    .finder-toolbar { display:flex; align-items:center; justify-content:space-between; gap:12px; min-height:50px; padding:8px 12px; border-bottom:1px solid var(--line); background:#fbfbfc; flex-wrap:wrap; }
    .finder-search { display:flex; align-items:center; gap:8px; flex:1 1 360px; min-width:260px; }
    .finder-search input[type="search"] { flex:1; border-radius:7px; }
    .finder-check { display:inline-flex; align-items:center; gap:6px; white-space:nowrap; color:#3d4554; }
    .finder-list { display:block; }
    .finder-head, .finder-row { display:grid; grid-template-columns:minmax(260px,1fr) minmax(86px,110px) minmax(150px,180px) minmax(170px,210px); }
    .finder-head span { background:#f6f7f9; color:#687084; font-size:12px; font-weight:600; padding:7px 10px; border-bottom:1px solid var(--line); }
    .finder-cell { height:34px; padding:6px 10px; border-bottom:1px solid #edf0f5; display:flex; align-items:center; min-width:0; overflow:hidden; white-space:nowrap; }
    .finder-row:hover .finder-cell { background:#eef5ff; }
    .finder-row.folder { cursor:pointer; }
    .finder-name { gap:7px; }
    .finder-name code { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; display:block; min-width:0; }
    .finder-chevron { width:22px; height:22px; display:inline-flex; align-items:center; justify-content:center; color:#4b5565; flex:0 0 auto; }
    .finder-icon { width:18px; text-align:center; flex:0 0 auto; }
    .finder-status-icon { width:18px; text-align:center; flex:0 0 auto; }
    .finder-name span:last-child { min-width:0; overflow:hidden; }
    .finder-path { display:none; }
    .finder-source { color:var(--muted); font-size:12px; overflow:hidden; text-overflow:ellipsis; }
    .finder-actions { justify-content:flex-end; gap:5px; }
    .finder-actions button { min-height:26px; padding:3px 7px; }
    .finder-child-slot { display:block; }
    .finder-empty { padding:20px; color:var(--muted); }
    .pill { display:inline-flex; gap:5px; align-items:center; border:1px solid var(--line); border-radius:999px; padding:2px 7px; white-space:nowrap; background:#fff; max-width:100%; overflow:hidden; text-overflow:ellipsis; }
    .status-synced, .status-mark-synced { color:#067647; }
    .status-upload, .status-local-only { color:var(--blue); }
    .status-download { color:#6d3fb8; }
    .status-conflict { color:var(--warn); }
    .status-error { color:var(--bad); }
    .status-trash-remote, .status-trash-local, .status-delete-remote, .status-delete-local, .status-deleted-local, .status-deleted-remote { color:var(--bad); }
    .status-remote-only-ignored, .status-ignored { color:var(--muted); }
    .msg { border:1px solid #a7d9c9; background:#ecfdf5; color:#065f46; border-radius:8px; padding:10px 12px; margin-bottom:12px; }
    .err { border:1px solid #f3b8b3; background:#fff5f5; color:var(--bad); border-radius:8px; padding:10px 12px; margin-bottom:12px; }
    .muted { color:var(--muted); }
    .actions { display:flex; gap:6px; flex-wrap:wrap; }
    .htmx-indicator { display:none; color:var(--muted); }
    .modal { position:fixed; inset:0; background:rgba(32,36,45,.35); display:grid; place-items:center; padding:20px; z-index:5; }
    .modal-card { background:#fff; border-radius:8px; padding:18px; width:min(440px,100%); box-shadow:0 20px 50px rgba(0,0,0,.25); }
    .modal-actions { display:flex; gap:8px; align-items:center; justify-content:flex-end; margin-top:14px; }
    @media (max-width:900px) { .finder-head, .finder-row { grid-template-columns:minmax(190px,1fr) minmax(80px,100px) minmax(130px,160px); } .finder-cell:nth-child(4n), .finder-head span:nth-child(4) { display:none; } }
    @media (max-width:760px) { main { padding:14px; } header { align-items:flex-start; flex-direction:column; } th:nth-child(4), td:nth-child(4), th:nth-child(5), td:nth-child(5) { display:none; } }
  </style>
</head>
<body>
  <header>
    <div class="brand">ysync</div>
	    <nav>
	      <a class="{{if eq .Active "tree"}}active{{end}}" href="/">Tree</a>
	      <a class="{{if eq .Active "operations"}}active{{end}}" href="/operations">Operations</a>
	      <a class="{{if eq .Active "settings"}}active{{end}}" href="/settings">Settings</a>
    </nav>
  </header>
  <main id="main">
    {{if eq .Active "tree"}}{{template "tree" .}}{{end}}
    {{if eq .Active "operations"}}{{template "operations" .}}{{end}}
    {{if eq .Active "settings"}}{{template "settings" .}}{{end}}
  </main>
  <div id="modal"></div>
  <div id="noop" hidden></div>
</body>
</html>
{{end}}

{{define "tree"}}
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
<section class="grid">
  <div class="metric"><div class="value">{{index .Snapshot.Counts "conflict"}}</div><div class="label">conflicts</div></div>
  <div class="metric"><div class="value small {{if .Snapshot.SyncError}}status-error{{end}}">{{if .Snapshot.SyncRunning}}Syncing{{else}}{{if .Snapshot.SyncError}}Error{{else}}Idle{{end}}{{end}}</div><div class="label">{{.Snapshot.SyncStatus}}</div></div>
  <div class="metric"><div class="value small">{{formatTime .Snapshot.LastSyncAt}}</div><div class="label">last sync</div></div>
  <div class="metric"><div class="value small">{{formatTime .Snapshot.RemoteAt}}</div><div class="label">remote metadata</div></div>
</section>
<section class="finder">
  <div class="finder-main">
	    <form class="finder-toolbar" method="get" action="/tree">
	      <div class="finder-search">
	        <input type="search" name="q" value="{{.Query}}" placeholder="Filter by name">
	        <label class="finder-check"><input type="checkbox" name="local_only" value="1" {{if .LocalOnly}}checked{{end}} onchange="this.form.submit()"> Local only</label>
	        <label class="finder-check"><input type="checkbox" name="conflicts" value="1" {{if .ConflictOnly}}checked{{end}} onchange="this.form.submit()"> Conflicts</label>
	      </div>
	      <div class="muted">{{len .Nodes}} items</div>
	    </form>
    <div class="finder-list">
      <div class="finder-head"><span>Name</span><span>Size</span><span>Modified</span><span>Actions</span></div>
      {{template "treeRows" .}}
    </div>
  </div>
</section>
{{end}}

{{define "treeRows"}}
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{range .Nodes}}
<div id="entry-{{.NodeID}}">
<div id="row-{{.NodeID}}" class="finder-row {{if .IsDir}}folder{{end}}" {{if .IsDir}}hx-get="{{.ChildrenURL}}" hx-trigger="click" hx-target="#children-{{.NodeID}}" hx-swap="innerHTML"{{end}}>
  <div class="finder-cell finder-name" style="padding-left: calc(10px + {{.Depth}} * 18px)">
    {{if .IsDir}}<span class="finder-chevron" title="Expand">&#9656;</span>{{else}}<span class="finder-chevron"></span>{{end}}
    <span class="finder-status-icon status-{{statusLabel .Status}}" title="{{statusLabel .Status}}">{{statusIcon .Status}}</span>
    <span class="finder-icon">{{if .IsDir}}&#128193;{{else}}&#128196;{{end}}</span>
    <span>
      <code>{{.Name}}</code>
      {{if .Path}}<span class="finder-path">{{.Path}}</span>{{end}}
    </span>
  </div>
  <div class="finder-cell">{{if .LocalExists}}{{formatSize .LocalSize}}{{else}}{{if .RemoteExists}}{{formatSize .RemoteSize}}{{else}}-{{end}}{{end}}</div>
  <div class="finder-cell">{{if .LocalExists}}{{formatTime .LocalModTime}}{{else}}{{formatTime .RemoteModTime}}{{end}}<span class="finder-source">&nbsp;{{if .LocalExists}}local{{end}}{{if and .LocalExists .RemoteExists}} + {{end}}{{if .RemoteExists}}remote{{end}}</span></div>
  <div class="finder-cell finder-actions">
    {{if conflictFile .}}
      <form class="sync-form" hx-post="/actions/path/upload?{{actionPath .Path "tree-row"}}" hx-target="#entry-{{.NodeID}}" hx-swap="outerHTML"><input type="hidden" name="path" value="{{.Path}}"><input type="hidden" name="return" value="tree-row"><button type="submit">Keep local</button></form>
      <form class="sync-form" hx-post="/actions/path/download?{{actionPath .Path "tree-row"}}" hx-target="#entry-{{.NodeID}}" hx-swap="outerHTML"><input type="hidden" name="path" value="{{.Path}}"><input type="hidden" name="return" value="tree-row"><button type="submit">Keep remote</button></form>
      {{if canMark .}}<form class="sync-form" hx-post="/actions/path/mark-synced?{{actionPath .Path "tree-row"}}" hx-target="#entry-{{.NodeID}}" hx-swap="outerHTML"><input type="hidden" name="path" value="{{.Path}}"><input type="hidden" name="return" value="tree-row"><button type="submit">Mark synced</button></form>{{end}}
    {{end}}
    {{if syncAction .}}<form class="sync-form" hx-post="/actions/path/{{syncAction .}}?{{actionPath .Path "tree-row"}}" hx-target="#entry-{{.NodeID}}" hx-swap="outerHTML"><input type="hidden" name="path" value="{{.Path}}"><input type="hidden" name="return" value="tree-row"><button type="submit">Sync</button></form>{{end}}
    {{if deleteAction .}}<button class="danger" hx-get="/partials/confirm?action={{deleteAction .}}&path={{query .Path}}&return=tree-row" hx-target="#modal">Delete</button>{{end}}
    {{if .LocalExists}}<form hx-post="/actions/path/open-local" hx-target="#noop" hx-swap="none"><input type="hidden" name="path" value="{{.Path}}"><button type="submit">Open</button></form>{{end}}
  </div>
</div>
{{if .IsDir}}<div class="finder-child-slot" id="children-{{.NodeID}}"></div>{{end}}
</div>
{{else}}
<div class="finder-child-slot finder-empty">No matching files or folders.</div>
{{end}}
{{end}}

{{define "operations"}}
<div class="bar"><h1>Current Session Operations</h1></div>
<table><thead><tr><th>Time</th><th>Action</th><th>Path</th><th>Message</th></tr></thead><tbody>
{{range .Ops}}<tr><td>{{formatTime .At}}</td><td>{{.Action}}</td><td><code>{{.Path}}</code></td><td>{{if .Error}}<span class="status-error">! {{.Message}}</span>{{else}}{{.Message}}{{end}}</td></tr>{{else}}<tr><td colspan="4" class="muted">No UI operations yet.</td></tr>{{end}}
</tbody></table>
{{end}}

{{define "settings"}}
<div class="bar"><h1>Settings</h1></div>
<table><tbody>
<tr><th>Local root</th><td><code>{{.Settings.LocalRoot}}</code></td></tr>
<tr><th>Remote root</th><td><code>{{.Settings.RemoteRoot}}</code></td></tr>
<tr><th>Base URL</th><td><code>{{.Settings.BaseURL}}</code></td></tr>
<tr><th>Import remote</th><td>{{.Settings.ImportRemote}}</td></tr>
<tr><th>Delete mode</th><td>{{.Settings.DeleteMode}}</td></tr>
<tr><th>Remote cache TTL</th><td>{{.Settings.Interval}}</td></tr>
<tr><th>UI address</th><td><code>{{.Settings.UIAddr}}</code></td></tr>
</tbody></table>
{{end}}

{{define "confirm"}}
<div class="modal">
  <div class="modal-card">
    <h2>Confirm {{.Confirm.Action}}</h2>
    <p>This action is bound to <code>{{.Confirm.Path}}</code> and expires shortly.</p>
    <div class="modal-actions">
      <form hx-post="{{.Confirm.URL}}" hx-target="{{.Confirm.Target}}" {{if .Confirm.Swap}}hx-swap="{{.Confirm.Swap}}"{{end}} style="display:contents">
        <input type="hidden" name="path" value="{{.Confirm.Path}}">
        {{if .Confirm.Return}}<input type="hidden" name="return" value="{{.Confirm.Return}}">{{end}}
        <input type="hidden" name="confirm_token" value="{{.Confirm.Token}}">
        <button class="danger" type="submit">Confirm</button>
      </form>
      <button type="button" data-no-ajax onclick="event.preventDefault(); document.getElementById('modal').innerHTML=''">Cancel</button>
    </div>
  </div>
</div>
{{end}}
`
