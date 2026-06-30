package ysync

import (
	"context"
	"net/http"
	"testing"
)

func TestWebDAVMoveOverwriteHeaders(t *testing.T) {
	client, requests := testWebDAVClient(t)
	_, _ = client.MoveUploadTempToFinal(context.Background(), "docs/a.txt.ysync-uploading", "docs/a.txt")
	if got := lastRequest(requests).Header.Get("Overwrite"); got != "T" {
		t.Fatalf("upload final MOVE overwrite = %q, want T", got)
	}

	_, _ = client.MoveOriginalToConflict(context.Background(), "docs/a.txt", "docs/a.txt.conflict-remote-20260628T120000Z")
	if got := lastRequest(requests).Header.Get("Overwrite"); got != "F" {
		t.Fatalf("conflict MOVE overwrite = %q, want F", got)
	}

	_, _ = client.MoveOriginalToTrash(context.Background(), "docs/a.txt", "/ysync-trash/20260628/docs/a.txt")
	if got := lastRequest(requests).Header.Get("Overwrite"); got != "F" {
		t.Fatalf("trash MOVE overwrite = %q, want F", got)
	}
}

func TestWebDAVDestinationHeaderIsEscaped(t *testing.T) {
	client, requests := testWebDAVClient(t)
	_, _ = client.MoveUploadTempToFinal(context.Background(), "docs/a.txt.ysync-uploading", "docs/a file.txt")
	got := lastRequest(requests).Header.Get("Destination")
	want := "https://example.test/YSync/docs/a%20file.txt"
	if got != want {
		t.Fatalf("Destination = %q, want %q", got, want)
	}
}

func TestWebDAVAbsoluteDestinationBypassesRemoteRoot(t *testing.T) {
	client, requests := testWebDAVClient(t)
	_, _ = client.MoveOriginalToTrash(context.Background(), "docs/a.txt", "/ysync-trash/20260628/docs/a.txt")
	got := lastRequest(requests).Header.Get("Destination")
	want := "https://example.test/ysync-trash/20260628/docs/a.txt"
	if got != want {
		t.Fatalf("Destination = %q, want %q", got, want)
	}
}

func TestWebDAVBasicAuth(t *testing.T) {
	client, requests := testWebDAVClient(t)
	client.User = "login@yandex.ru"
	client.Password = "app-secret"
	_, _ = client.Get(context.Background(), "docs/a.txt", 0)
	user, pass, ok := lastRequest(requests).BasicAuth()
	if !ok || user != "login@yandex.ru" || pass != "app-secret" {
		t.Fatalf("BasicAuth = %q/%q/%v", user, pass, ok)
	}
}

func TestWebDAVGetRange(t *testing.T) {
	client, requests := testWebDAVClient(t)
	_, _ = client.Get(context.Background(), "docs/a.txt", 42)
	if got := lastRequest(requests).Header.Get("Range"); got != "bytes=42-" {
		t.Fatalf("Range = %q", got)
	}
}

func testWebDAVClient(t *testing.T) (*WebDAVClient, *[]*http.Request) {
	t.Helper()
	var requests []*http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client, &requests
}

func lastRequest(requests *[]*http.Request) *http.Request {
	return (*requests)[len(*requests)-1]
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}