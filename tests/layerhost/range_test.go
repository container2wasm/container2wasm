package layerhost

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func shouldUseRangeFetch(headOk bool, acceptRanges string, contentLength int64, isGzipN uint32) bool {
	return headOk && acceptRanges == "bytes" && contentLength > 0 && isGzipN == 0
}

func buildRangeHeader(offset, length uint32) string {
	end := offset + length - 1
	return "bytes=" + strconv.FormatUint(uint64(offset), 10) + "-" + strconv.FormatUint(uint64(end), 10)
}

func TestShouldUseRangeFetch(t *testing.T) {
	if !shouldUseRangeFetch(true, "bytes", 1000, 0) {
		t.Fatal("expected range fetch for byte-capable layer")
	}
	if shouldUseRangeFetch(true, "none", 1000, 0) {
		t.Fatal("expected no range fetch without Accept-Ranges bytes")
	}
	if shouldUseRangeFetch(true, "bytes", 0, 0) {
		t.Fatal("expected no range fetch for zero length")
	}
	if shouldUseRangeFetch(true, "bytes", 1000, 1) {
		t.Fatal("expected no range fetch for gzip layer")
	}
}

func TestBuildRangeHeader(t *testing.T) {
	got := buildRangeHeader(100, 100)
	if got != "bytes=100-199" {
		t.Fatalf("unexpected range header: %q", got)
	}
}

func TestRangeServerServesPartialContent(t *testing.T) {
	body := []byte("0123456789abcdef")
	var fullReads int
	var rangeReads int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			return
		}
		if strings.HasPrefix(r.Header.Get("Range"), "bytes=") {
			rangeReads++
			rangeHeader := r.Header.Get("Range")
			if rangeHeader != "bytes=4-7" {
				t.Fatalf("unexpected range request: %q", rangeHeader)
			}
			w.Header().Set("Content-Range", "bytes 4-7/16")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[4:8])
			return
		}
		fullReads++
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	headResp, err := http.Head(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldUseRangeFetch(headResp.StatusCode == http.StatusOK, headResp.Header.Get("Accept-Ranges"), headResp.ContentLength, 0) {
		t.Fatal("test server should advertise range fetch")
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", buildRangeHeader(4, 4))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "4567" {
		t.Fatalf("unexpected range payload: %q", string(data))
	}
	if rangeReads != 1 {
		t.Fatalf("expected one range read, got %d", rangeReads)
	}
	if fullReads != 0 {
		t.Fatalf("expected no full-body read, got %d", fullReads)
	}
}
