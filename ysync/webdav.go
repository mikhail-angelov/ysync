package ysync

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type WebDAVClient struct {
	BaseURL    *url.URL
	RemoteRoot string
	User       string
	Password   string
	HTTP       *http.Client
	Log        Logger
}

func NewWebDAVClient(baseURL, remoteRoot, user, password string, httpClient *http.Client) (*WebDAVClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &WebDAVClient{
		BaseURL:    u,
		RemoteRoot: cleanRemoteRoot(remoteRoot),
		User:       user,
		Password:   password,
		HTTP:       httpClient,
	}, nil
}

func (c *WebDAVClient) Propfind(ctx context.Context, relPath string, depth string) (*http.Response, error) {
	req, err := c.newRequest(ctx, "PROPFIND", relPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", depth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	return c.do(req, relPath)
}

func (c *WebDAVClient) Get(ctx context.Context, relPath string, startAt int64) (*http.Response, error) {
	req, err := c.newRequest(ctx, http.MethodGet, relPath, nil)
	if err != nil {
		return nil, err
	}
	if startAt > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startAt))
	}
	return c.do(req, relPath)
}

func (c *WebDAVClient) Put(ctx context.Context, relPath string, body io.Reader) (*http.Response, error) {
	req, err := c.newRequest(ctx, http.MethodPut, relPath, body)
	if err != nil {
		return nil, err
	}
	return c.do(req, relPath)
}

func (c *WebDAVClient) Delete(ctx context.Context, relPath string) (*http.Response, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, relPath, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req, relPath)
}

func (c *WebDAVClient) Move(ctx context.Context, fromRel, toRel string, overwrite bool) (*http.Response, error) {
	req, err := c.newRequest(ctx, "MOVE", fromRel, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Destination", c.remoteURL(toRel).String())
	if overwrite {
		req.Header.Set("Overwrite", "T")
	} else {
		req.Header.Set("Overwrite", "F")
	}
	return c.do(req, fromRel)
}

func (c *WebDAVClient) do(req *http.Request, relPath string) (*http.Response, error) {
	start := time.Now()
	if c.Log.out != nil {
		c.Log.Info("webdav request", "method", req.Method, "path", cleanRel(relPath), "depth", req.Header.Get("Depth"))
	}
	resp, err := c.HTTP.Do(req)
	if c.Log.out != nil {
		if err != nil {
			c.Log.Error("webdav response", "method", req.Method, "path", cleanRel(relPath), "duration", time.Since(start), "error", err)
		} else {
			c.Log.Info("webdav response", "method", req.Method, "path", cleanRel(relPath), "status", resp.Status, "duration", time.Since(start), "retry_after", resp.Header.Get("Retry-After"))
		}
	}
	return resp, err
}

func (c *WebDAVClient) MoveUploadTempToFinal(ctx context.Context, tempRel, finalRel string) (*http.Response, error) {
	return c.Move(ctx, tempRel, finalRel, true)
}

func (c *WebDAVClient) MoveOriginalToConflict(ctx context.Context, originalRel, conflictRel string) (*http.Response, error) {
	return c.Move(ctx, originalRel, conflictRel, false)
}

func (c *WebDAVClient) MoveOriginalToTrash(ctx context.Context, originalRel, trashRel string) (*http.Response, error) {
	return c.Move(ctx, originalRel, trashRel, false)
}

func (c *WebDAVClient) MoveStaleTempToTrash(ctx context.Context, tempRel, trashRel string) (*http.Response, error) {
	return c.Move(ctx, tempRel, trashRel, false)
}

func (c *WebDAVClient) newRequest(ctx context.Context, method, relPath string, body io.Reader) (*http.Request, error) {
	if body == nil {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.remoteURL(relPath).String(), body)
	if err != nil {
		return nil, err
	}
	if c.User != "" || c.Password != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	return req, nil
}

func (c *WebDAVClient) remoteURL(relPath string) *url.URL {
	u := *c.BaseURL
	parts := []string{cleanRemoteRoot(c.RemoteRoot)}
	if relPath != "" {
		if strings.HasPrefix(relPath, "/") {
			parts = []string{cleanRel(relPath)}
		} else {
			parts = append(parts, cleanRel(relPath))
		}
	}
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, segment := range strings.Split(part, "/") {
			if segment == "" {
				continue
			}
			segments = append(segments, segment)
		}
	}
	u.Path = "/" + strings.Join(segments, "/")
	return &u
}

func cleanRemoteRoot(remoteRoot string) string {
	remoteRoot = strings.TrimSpace(remoteRoot)
	if remoteRoot == "" || remoteRoot == "/" {
		return ""
	}
	return strings.TrimPrefix(path.Clean("/"+remoteRoot), "/")
}
