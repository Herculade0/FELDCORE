package files

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testFetcher(allow bool, lookup func(context.Context, string) ([]net.IP, error), dial func(context.Context, string, string) (net.Conn, error)) *Fetcher {
	f := NewFetcher(func() bool { return allow })
	f.SetTestHooks(lookup, dial)
	return f
}

func publicLookup(ip string) func(context.Context, string) ([]net.IP, error) {
	return func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(ip)}, nil
	}
}

func waitJob(t *testing.T, job *Job) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view := job.View()
		switch view.Status {
		case StatusDone, StatusError, StatusCancelled, StatusSkipped:
			return view
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job stuck at %+v", job.View())
	return Job{}
}

func TestFetchJobWritesFile(t *testing.T) {
	const body = "hello from far away"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "19")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	var dialed string
	f := testFetcher(false, publicLookup("203.0.113.10"), func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return net.Dial("tcp", srv.Listener.Addr().String())
	})
	root := t.TempDir()
	job, err := f.Start(Request{
		Root: root, Share: "media", Dir: "inbox", URL: "http://download.example/file.txt",
		Filename: "got.txt", Conflict: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	view := waitJob(t, job)
	if view.Status != StatusDone {
		t.Fatalf("status %s err %s", view.Status, view.Error)
	}
	if view.Bytes != int64(len(body)) || view.Total != int64(len(body)) {
		t.Fatalf("progress bytes=%d total=%d", view.Bytes, view.Total)
	}
	if dialed != "203.0.113.10:80" {
		t.Fatalf("dialed %s", dialed)
	}
	got, err := os.ReadFile(filepath.Join(root, "inbox", "got.txt"))
	if err != nil || string(got) != body {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, UploadsDir)); err != nil {
		t.Fatal("uploads dir missing", err)
	}
	dents, _ := os.ReadDir(filepath.Join(root, UploadsDir))
	if len(dents) != 0 {
		t.Fatalf("partial left behind: %v", dents)
	}
}

func TestFetchBlocksLoopbackMetadataAndRedirect(t *testing.T) {
	root := t.TempDir()
	f := NewFetcher(func() bool { return true })
	for _, raw := range []string{
		"http://127.0.0.1/secret",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"file:///etc/passwd",
		"http://[::1]/",
	} {
		if _, err := f.Start(Request{Root: root, Share: "media", URL: raw}); err == nil {
			t.Errorf("started %s", raw)
		}
		if dents, _ := os.ReadDir(root); len(dents) != 0 {
			t.Fatalf("blocked download wrote %v", dents)
		}
	}

	var dials int
	blocked := testFetcher(true, publicLookup("169.254.169.254"), func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("should not dial")
	})
	job, err := blocked.Start(Request{Root: root, Share: "media", URL: "http://public.example/meta"})
	if err != nil {
		t.Fatal(err)
	}
	view := waitJob(t, job)
	if view.Status != StatusError || dials != 0 {
		t.Fatalf("status %s dials %d err %s", view.Status, dials, view.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "meta")); !os.IsNotExist(err) {
		t.Fatal("metadata download created a file")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	redir := testFetcher(false, publicLookup("203.0.113.10"), func(context.Context, string, string) (net.Conn, error) {
		return net.Dial("tcp", srv.Listener.Addr().String())
	})
	job, err = redir.Start(Request{Root: root, Share: "media", URL: "http://download.example/go"})
	if err != nil {
		t.Fatal(err)
	}
	view = waitJob(t, job)
	if view.Status != StatusError {
		t.Fatalf("redirect status %s", view.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "go")); !os.IsNotExist(err) {
		t.Fatal("redirect wrote a file")
	}
}

func TestFetchLANSetting(t *testing.T) {
	root := t.TempDir()
	var dials int
	var mu sync.Mutex
	dial := func(context.Context, string, string) (net.Conn, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return nil, errors.New("no such host")
	}
	off := testFetcher(false, publicLookup("10.1.2.3"), dial)
	job, err := off.Start(Request{Root: root, Share: "media", URL: "http://nas.example/a.bin", Filename: "a.bin"})
	if err != nil {
		t.Fatal(err)
	}
	view := waitJob(t, job)
	if view.Status != StatusError || dials != 0 {
		t.Fatalf("LAN off: status %s dials %d err %s", view.Status, dials, view.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "a.bin")); !os.IsNotExist(err) {
		t.Fatal("LAN download wrote a file")
	}

	on := testFetcher(true, publicLookup("10.1.2.3"), dial)
	job, err = on.Start(Request{Root: root, Share: "media", URL: "http://nas.example/a.bin", Filename: "a.bin"})
	if err != nil {
		t.Fatal(err)
	}
	view = waitJob(t, job)
	mu.Lock()
	n := dials
	mu.Unlock()
	if n != 1 || view.Status != StatusError {
		t.Fatalf("LAN on: status %s dials %d err %s", view.Status, n, view.Error)
	}
}

func TestFetchCancelRemovesPartial(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "200000")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write(bytesRepeat(4000))
		flusher.Flush()
		close(started)
		<-block
	}))
	defer srv.Close()
	defer close(block)

	f := testFetcher(false, publicLookup("203.0.113.10"), func(context.Context, string, string) (net.Conn, error) {
		return net.Dial("tcp", srv.Listener.Addr().String())
	})
	root := t.TempDir()
	job, err := f.Start(Request{Root: root, Share: "media", URL: "http://download.example/slow.bin", Filename: "slow.bin"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	// Give the client a moment to store the first bytes.
	time.Sleep(50 * time.Millisecond)
	if err := f.Cancel("media", job.ID); err != nil {
		t.Fatal(err)
	}
	view := waitJob(t, job)
	if view.Status != StatusCancelled {
		t.Fatalf("status %s err %s", view.Status, view.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "slow.bin")); !os.IsNotExist(err) {
		t.Fatal("cancelled download left the file", err)
	}
}

func bytesRepeat(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return b
}
