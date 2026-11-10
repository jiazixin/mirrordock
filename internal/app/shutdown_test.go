package app

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	runengine "mirrordock/internal/run"
)

// A public download that outlasts the drain deadline must not keep the run
// engine, the notification worker and the storage from shutting down cleanly.
func TestCloseStopsWorkersWhileDownloadsDrain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: shutdown-fixture\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestApp(t, a)
	// A sparse file far larger than the socket buffers keeps the response
	// blocked once the client stops reading.
	const stalledBytes = 256 << 20
	large, err := os.Create(filepath.Join(a.Mirrors.Root, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(large.Truncate(stalledBytes), large.Close()); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- a.Server.Serve(listener) }()
	client, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(client, "GET /large.bin HTTP/1.1\r\nHost: mirror.test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// The first bytes prove the download is in flight; nothing more is read.
	if _, err := io.ReadFull(client, make([]byte, 16)); err != nil {
		t.Fatal(err)
	}

	const drainDeadline = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), drainDeadline)
	defer cancel()
	if err := a.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got close error %v, want the drain deadline", err)
	}
	if err := <-served; err == nil {
		t.Fatal("server kept serving after Close")
	}
	if _, err := a.Runs.Enqueue(t.Context(), "missing", "manual"); !errors.Is(err, runengine.ErrStopped) {
		t.Fatalf("got enqueue error %v, want %v", err, runengine.ErrStopped)
	}
	if _, err := a.Store.State(t.Context(), "last_config_change"); err == nil {
		t.Fatal("storage left open after the drain deadline")
	}
}
