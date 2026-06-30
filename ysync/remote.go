package ysync

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	remoteRetryAttempts = 3
	remoteRetryBackoff  = time.Second
)

func LoadRemoteTree(ctx context.Context, client *WebDAVClient, workers int, log Logger) (map[string]RemoteEntry, error) {
	if workers < 1 {
		workers = 1
	}
	log.Debug("loading remote tree with recursive Depth 1 crawl")
	return crawlRemoteDepth1(ctx, client, workers)
}

func RefreshRemotePath(ctx context.Context, client *WebDAVClient, relPath string) (RemoteEntry, bool, error) {
	entries, err := propfindEntries(ctx, client, relPath, "0")
	if err != nil {
		var statusErr remoteStatusError
		if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusNotFound {
			return RemoteEntry{}, false, nil
		}
		return RemoteEntry{}, false, err
	}
	entry, ok := entries[cleanRel(relPath)]
	return entry, ok, nil
}

func propfindEntries(ctx context.Context, client *WebDAVClient, relPath, depth string) (map[string]RemoteEntry, error) {
	var lastErr error
	for attempt := 1; attempt <= remoteRetryAttempts; attempt++ {
		entries, err := propfindEntriesOnce(ctx, client, relPath, depth)
		if err == nil {
			return entries, nil
		}
		if !shouldRetryRemote(err) || attempt == remoteRetryAttempts {
			return nil, err
		}
		lastErr = err
		if err := sleepWithContext(ctx, retryDelay(err, attempt)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func propfindEntriesOnce(ctx context.Context, client *WebDAVClient, relPath, depth string) (map[string]RemoteEntry, error) {
	resp, err := client.Propfind(ctx, relPath, depth)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter, hasRetryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return nil, remoteStatusError{relPath: relPath, depth: depth, status: resp.Status, statusCode: resp.StatusCode, retryAfter: retryAfter, hasRetryAfter: hasRetryAfter}
	}
	return parsePropfind(resp.Body, client.RemoteRoot)
}

type remoteStatusError struct {
	relPath       string
	depth         string
	status        string
	statusCode    int
	retryAfter    time.Duration
	hasRetryAfter bool
}

func (e remoteStatusError) Error() string {
	return fmt.Sprintf("PROPFIND %s depth=%s failed: %s", e.relPath, e.depth, e.status)
}

func shouldRetryRemote(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr remoteStatusError
	if errors.As(err, &statusErr) {
		return statusErr.statusCode == http.StatusRequestTimeout ||
			statusErr.statusCode == http.StatusTooManyRequests ||
			statusErr.statusCode >= 500
	}
	return true
}

func retryDelay(err error, attempt int) time.Duration {
	var statusErr remoteStatusError
	if errors.As(err, &statusErr) && statusErr.hasRetryAfter {
		return statusErr.retryAfter
	}
	delay := remoteRetryBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if !at.After(now) {
		return 0, true
	}
	return at.Sub(now), true
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func crawlRemoteDepth1(ctx context.Context, client *WebDAVClient, workers int) (map[string]RemoteEntry, error) {
	type result struct {
		path    string
		entries map[string]RemoteEntry
		err     error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string, workers)
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range jobs {
				entries, err := propfindEntries(ctx, client, rel, "1")
				results <- result{path: rel, entries: entries, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	pending := []string{""}
	inflight := 0
	all := map[string]RemoteEntry{}
	for len(pending) > 0 || inflight > 0 {
		for len(pending) > 0 && inflight < workers && ctx.Err() == nil {
			next := pending[0]
			pending = pending[1:]
			inflight++
			select {
			case jobs <- next:
			case <-ctx.Done():
				close(jobs)
				return nil, ctx.Err()
			}
		}
		res, ok := <-results
		if !ok {
			break
		}
		inflight--
		if res.err != nil {
			close(jobs)
			return nil, res.err
		}
		for p, entry := range res.entries {
			if p == "" {
				continue
			}
			if _, exists := all[p]; !exists {
				all[p] = entry
				if entry.IsDir && p != res.path {
					pending = append(pending, p)
				}
			}
		}
	}
	close(jobs)
	return all, nil
}

type multistatusXML struct {
	Responses []responseXML `xml:"response"`
}

type responseXML struct {
	HRef     string        `xml:"href"`
	Propstat []propstatXML `xml:"propstat"`
}

type propstatXML struct {
	Prop propXML `xml:"prop"`
}

type propXML struct {
	ResourceType  resourceTypeXML `xml:"resourcetype"`
	LastModified  string          `xml:"getlastmodified"`
	ContentLength string          `xml:"getcontentlength"`
	ETag          string          `xml:"getetag"`
}

type resourceTypeXML struct {
	Collection *struct{} `xml:"collection"`
}

func parsePropfind(r io.Reader, remoteRoot string) (map[string]RemoteEntry, error) {
	var ms multistatusXML
	if err := xml.NewDecoder(r).Decode(&ms); err != nil {
		return nil, err
	}
	entries := map[string]RemoteEntry{}
	for _, resp := range ms.Responses {
		entry, ok, err := responseToRemoteEntry(resp, remoteRoot)
		if err != nil {
			return nil, err
		}
		if ok {
			entries[entry.Path] = entry
		}
	}
	return entries, nil
}

func responseToRemoteEntry(resp responseXML, remoteRoot string) (RemoteEntry, bool, error) {
	rel, ok, err := remoteRelFromHref(resp.HRef, remoteRoot)
	if err != nil || !ok {
		return RemoteEntry{}, ok, err
	}
	prop := propXML{}
	for _, propstat := range resp.Propstat {
		prop = propstat.Prop
		break
	}
	isDir := prop.ResourceType.Collection != nil
	size := int64(0)
	if prop.ContentLength != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(prop.ContentLength), 10, 64)
		if err != nil {
			return RemoteEntry{}, false, err
		}
		size = parsed
	}
	modTime := time.Time{}
	if strings.TrimSpace(prop.LastModified) != "" {
		parsed, err := http.ParseTime(strings.TrimSpace(prop.LastModified))
		if err != nil {
			return RemoteEntry{}, false, err
		}
		modTime = normalizeTime(parsed)
	}
	return RemoteEntry{
		Path:    rel,
		IsDir:   isDir,
		ModTime: modTime,
		Size:    size,
		ETag:    strings.Trim(prop.ETag, "\""),
	}, true, nil
}

func remoteRelFromHref(href, remoteRoot string) (string, bool, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", false, err
	}
	hrefPath, err := unescapePathSegments(u.EscapedPath())
	if err != nil {
		return "", false, err
	}
	hrefPath = strings.TrimPrefix(path.Clean("/"+hrefPath), "/")
	root := cleanRemoteRoot(remoteRoot)
	if root != "" {
		if hrefPath == root {
			return "", false, nil
		}
		prefix := root + "/"
		if !strings.HasPrefix(hrefPath, prefix) {
			return "", false, nil
		}
		hrefPath = strings.TrimPrefix(hrefPath, prefix)
	}
	if hrefPath == "." || hrefPath == "" {
		return "", false, nil
	}
	return cleanRel(hrefPath), true, nil
}

func unescapePathSegments(rawPath string) (string, error) {
	segments := strings.Split(rawPath, "/")
	decoded := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		part, err := url.PathUnescape(segment)
		if err != nil {
			return "", err
		}
		decoded = append(decoded, part)
	}
	return strings.Join(decoded, "/"), nil
}
