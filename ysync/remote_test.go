package ysync

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParsePropfindEntries(t *testing.T) {
	xml := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/YSync/</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat>
  </d:response>
  <d:response>
    <d:href>/YSync/docs/a%20file.txt</d:href>
    <d:propstat><d:prop>
      <d:getlastmodified>Sun, 28 Jun 2026 10:00:00 GMT</d:getlastmodified>
      <d:getcontentlength>42</d:getcontentlength>
      <d:getetag>"abc"</d:getetag>
    </d:prop></d:propstat>
  </d:response>
</d:multistatus>`
	entries, err := parsePropfind(strings.NewReader(xml), "/YSync")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := entries["docs/a file.txt"]
	if !ok {
		t.Fatalf("entry not found: %#v", entries)
	}
	if entry.Size != 42 || entry.ETag != "abc" || entry.IsDir {
		t.Fatalf("bad entry: %#v", entry)
	}
}

func TestParsePropfindNormalizesUnicodePathKeys(t *testing.T) {
	xml := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/YSync/%D0%A1%D0%BF%D0%B8%D1%81%D0%BE%D0%BA%20%D0%90%D0%9A%D0%A0%20%D1%82%D0%B5%D0%BA%D1%81%D1%82%D0%BE%D0%B2%D1%8B%D0%B8%CC%86.pdf</d:href>
    <d:propstat><d:prop>
      <d:getcontentlength>42</d:getcontentlength>
    </d:prop></d:propstat>
  </d:response>
</d:multistatus>`
	entries, err := parsePropfind(strings.NewReader(xml), "/YSync")
	if err != nil {
		t.Fatal(err)
	}
	want := "Список АКР текстовый.pdf"
	if _, ok := entries[want]; !ok {
		t.Fatalf("normalized path %q not found in %#v", want, entries)
	}
}

func TestParsePropfindPreservesEscapedSlashInFilename(t *testing.T) {
	xml := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/YSync/invoces/%D0%B0%D0%BA%D1%82%20%D0%98%D0%9F%20%D0%90%D0%BD%D0%B3%D0%B5%D0%BB%D0%BE%D0%B2-10%252F18.pdf</d:href>
    <d:propstat><d:prop>
      <d:getcontentlength>132841</d:getcontentlength>
    </d:prop></d:propstat>
  </d:response>
</d:multistatus>`
	entries, err := parsePropfind(strings.NewReader(xml), "/YSync")
	if err != nil {
		t.Fatal(err)
	}
	want := "invoces/акт ИП Ангелов-10%2F18.pdf"
	if _, ok := entries[want]; !ok {
		t.Fatalf("escaped slash filename %q not found in %#v", want, entries)
	}
	if _, ok := entries["invoces/акт ИП Ангелов-10/18.pdf"]; ok {
		t.Fatalf("escaped slash was treated as a path separator: %#v", entries)
	}
}

func TestLoadRemoteTreeCrawlsDepth1Recursively(t *testing.T) {
	var depths []string
	var paths []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		depth := req.Header.Get("Depth")
		depths = append(depths, depth)
		paths = append(paths, req.URL.Path)
		body := ``
		switch req.URL.Path {
		case "/YSync":
			body = `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>
<d:response><d:href>/YSync/docs/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>
</d:multistatus>`
		case "/YSync/docs":
			body = `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/docs/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>
<d:response><d:href>/YSync/docs/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>1</d:getcontentlength></d:prop></d:propstat></d:response>
</d:multistatus>`
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusMultiStatus, Status: "207 Multi-Status", Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := LoadRemoteTree(context.Background(), client, 2, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries["docs/a.txt"]; !ok {
		t.Fatalf("nested entry not found: %#v", entries)
	}
	if len(depths) != 2 || depths[0] != "1" || depths[1] != "1" {
		t.Fatalf("depth sequence = %#v", depths)
	}
	wantPaths := []string{"/YSync", "/YSync/docs"}
	for i := range wantPaths {
		if paths[i] != wantPaths[i] {
			t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
		}
	}
}

func TestLoadRemoteTreeRetriesTransientDepth1Failure(t *testing.T) {
	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>1</d:getcontentlength></d:prop></d:propstat></d:response>
</d:multistatus>`
		return &http.Response{StatusCode: http.StatusMultiStatus, Status: "207 Multi-Status", Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := LoadRemoteTree(context.Background(), client, 1, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if _, ok := entries["a.txt"]; !ok {
		t.Fatalf("entry not found: %#v", entries)
	}
}

func TestLoadRemoteTreeHonorsRetryAfterForRateLimit(t *testing.T) {
	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     http.Header{"Retry-After": []string{"0"}},
			}, nil
		}
		body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/YSync/a.txt</d:href><d:propstat><d:prop><d:getcontentlength>1</d:getcontentlength></d:prop></d:propstat></d:response>
</d:multistatus>`
		return &http.Response{StatusCode: http.StatusMultiStatus, Status: "207 Multi-Status", Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := LoadRemoteTree(context.Background(), client, 1, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if _, ok := entries["a.txt"]; !ok {
		t.Fatalf("entry not found: %#v", entries)
	}
}

func TestLoadRemoteTreeDoesNotRetryDepth1Forbidden(t *testing.T) {
	var depths []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		depth := req.Header.Get("Depth")
		depths = append(depths, depth)
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRemoteTree(context.Background(), client, 1, Logger{out: io.Discard}); err == nil {
		t.Fatal("expected forbidden error")
	}
	want := []string{"1"}
	if len(depths) != len(want) {
		t.Fatalf("depths = %#v, want %#v", depths, want)
	}
	for i := range want {
		if depths[i] != want[i] {
			t.Fatalf("depths = %#v, want %#v", depths, want)
		}
	}
}
