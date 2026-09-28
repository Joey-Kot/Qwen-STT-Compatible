package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("dependency source\n")
	if err := tw.WriteHeader(&tar.Header{Name: "source/README", Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runDownload(t *testing.T, url, out, digest string, env ...string) (string, error) {
	t.Helper()
	for _, tool := range []string{"sh", "curl", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("download integration tests require %s", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "download-file.sh", url, out, digest)
	cmd.Env = append(os.Environ(),
		"DOWNLOAD_MAX_ATTEMPTS=3", "DOWNLOAD_RETRY_DELAY=0",
		"DOWNLOAD_CONNECT_TIMEOUT=2", "DOWNLOAD_MAX_TIME=3",
		"NO_PROXY=*", "no_proxy=*",
	)
	cmd.Env = append(cmd.Env, env...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeDownload(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertDownload(t *testing.T, out string, data []byte) {
	t.Helper()
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("download mismatch: got %d bytes, want %d; err=%v", len(got), len(data), err)
	}
	assertAbsent(t, out+".part")
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should be absent: %v", path, err)
	}
}

func TestDownloadResumesInterruptedTransfer(t *testing.T) {
	data := testArchive(t)
	half := len(data) / 2
	out := filepath.Join(t.TempDir(), "path with spaces", "source.tar.gz")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No partial download may be published as a cache hit.
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("partial download published: %v", err)
		}
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Write(data[:half]) // Deliberately close before the declared length.
			return
		}
		if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", half) {
			t.Errorf("resume Range = %q", got)
		}
		http.ServeContent(w, r, "source.tar.gz", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()
	if log, err := runDownload(t, server.URL, out, ""); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	assertDownload(t, out, data)
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestDownloadRecoversCache(t *testing.T) {
	data := testArchive(t)
	for _, mode := range []string{"valid", "legacy partial", "part", "complete part", "corrupt", "no range"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "source.tar.gz")
			initial := data[:len(data)/2]
			name := out
			wantRequests := int32(1)
			switch mode {
			case "valid":
				initial, wantRequests = data, 0
			case "part":
				name += ".part"
			case "complete part":
				name, initial = out+".part", data
			case "corrupt":
				initial, wantRequests = bytes.Repeat([]byte("x"), len(data)+1), 2
			case "no range":
				wantRequests = 2
			}
			writeDownload(t, name, initial)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				wantRange := ""
				if n == 1 {
					wantRange = fmt.Sprintf("bytes=%d-", len(initial))
				}
				if got := r.Header.Get("Range"); got != wantRange {
					t.Errorf("request %d: Range=%q, want %q", n, got, wantRange)
				}
				if mode == "no range" {
					w.Header().Set("Content-Length", fmt.Sprint(len(data)))
					w.Write(data) // Ignore Range, like a server without resume support.
					return
				}
				http.ServeContent(w, r, "source.tar.gz", time.Time{}, bytes.NewReader(data))
			}))
			defer server.Close()
			if log, err := runDownload(t, server.URL, out, ""); err != nil {
				t.Fatalf("%v\n%s", err, log)
			}
			assertDownload(t, out, data)
			if requests.Load() != wantRequests {
				t.Fatalf("requests=%d, want %d", requests.Load(), wantRequests)
			}
		})
	}
}

func TestDownloadHTTPFailures(t *testing.T) {
	data := testArchive(t)
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusNotFound} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "source.tar.gz")
			partial := data[:len(data)/2]
			writeDownload(t, out+".part", partial)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "error body must not be appended", code)
			}))
			defer server.Close()
			log, err := runDownload(t, server.URL, out, "")
			if err == nil {
				t.Fatalf("HTTP %d should fail\n%s", code, log)
			}
			assertAbsent(t, out)
			got, err := os.ReadFile(out + ".part")
			if err != nil || !bytes.Equal(got, partial) {
				t.Fatalf("partial file not preserved: %v", err)
			}
			wantRequests := int32(3)
			if code == http.StatusNotFound {
				wantRequests = 1
			}
			if requests.Load() != wantRequests {
				t.Fatalf("requests=%d, want %d\n%s", requests.Load(), wantRequests, log)
			}
		})
	}
}

func TestDownloadRetriesTransientHTTPError(t *testing.T) {
	data := testArchive(t)
	out := filepath.Join(t.TempDir(), "source.tar.gz")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write(data)
	}))
	defer server.Close()
	if log, err := runDownload(t, server.URL, out, ""); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	assertDownload(t, out, data)
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want 2", requests.Load())
	}
}

func TestDownloadRejectsInvalidContent(t *testing.T) {
	data := testArchive(t)
	for _, mode := range []string{"html", "truncated gzip", "checksum", "truncated armor"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "source.tar.gz")
			body, digest := data, ""
			switch mode {
			case "html":
				body = []byte("<html>upstream unavailable</html>")
			case "truncated gzip":
				body = data[:len(data)-4] // Missing gzip trailer despite HTTP success.
			case "checksum":
				digest = strings.Repeat("0", 64)
				writeDownload(t, out, data) // A structurally valid cache must be checked too.
			case "truncated armor":
				out += ".asc"
				body = []byte("-----BEGIN PGP SIGNATURE-----\n\npartial\n")
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Write(body)
			}))
			defer server.Close()
			if log, err := runDownload(t, server.URL, out, digest); err == nil {
				t.Fatalf("invalid content accepted\n%s", log)
			}
			assertAbsent(t, out)
			assertAbsent(t, out+".part")
			if requests.Load() != 3 {
				t.Fatalf("requests=%d, want 3", requests.Load())
			}
		})
	}
}

func TestDownloadAcceptsChecksumAndArmor(t *testing.T) {
	data := testArchive(t)
	for _, mode := range []string{"checksum", "signature", "key"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "source.tar.gz")
			body, digest := data, fmt.Sprintf("%x", sha256.Sum256(data))
			if mode != "checksum" {
				kind := "SIGNATURE"
				if mode == "key" {
					kind = "PUBLIC KEY BLOCK"
				}
				out, digest = out+".asc", ""
				body = []byte(fmt.Sprintf("-----BEGIN PGP %s-----\r\n\r\nfixture\r\n-----END PGP %s-----\r\n", kind, kind))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			}))
			defer server.Close()
			if log, err := runDownload(t, server.URL, out, digest); err != nil {
				t.Fatalf("%v\n%s", err, log)
			}
			assertDownload(t, out, body)
		})
	}
}
