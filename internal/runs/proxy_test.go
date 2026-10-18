package runs

import (
	"context"
	"fmt"
	"io"
	"mirrordock/internal/config"
	syncer "mirrordock/internal/sync"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRsyncProxyIsResolvedPerAttemptAndSkipsMirrors(t *testing.T) {
	const global = "socks5://user:global-secret@127.0.0.1:1080"
	const custom = "http://user:custom-secret@127.0.0.1:8081"
	var mu sync.Mutex
	var proxies, candidates []string
	failProxy := true
	e, cfg, store, _ := testEngine(t, "5s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		proxies = append(proxies, task.Proxy)
		candidates = append(candidates, task.Candidate)
		fmt.Fprintln(log, "proxy", task.Proxy)
		if failProxy {
			failProxy = false
			return &syncer.Failure{Code: "RSYNC_PROXY", Message: "proxy down " + task.Proxy, Retryable: true, ProxyFailure: true}
		}
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0644)
	}), func(e *Engine) { e.retryDelay = time.Millisecond })
	update := func(mode, proxy string) {
		t.Helper()
		c := config.Clone(cfg.Current())
		c.Settings.Rsync.Proxy = global
		c.Sources[0].Rsync.ProxyMode, c.Sources[0].Rsync.Proxy = mode, proxy
		if err := cfg.Save(c, cfg.View().Revision, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	run := func() *Run {
		t.Helper()
		r, err := e.Enqueue("test", "manual")
		if err != nil {
			t.Fatal(err)
		}
		return waitState(t, e, r.ID, "succeeded")
	}

	update("", "")
	r := run()
	mu.Lock()
	// A failed proxy retries the round instead of trying src2 through it.
	if len(candidates) != 2 || !strings.HasSuffix(candidates[0], "/one/") || !strings.HasSuffix(candidates[1], "/one/") {
		t.Fatalf("proxy failure switched mirrors: %v", candidates)
	}
	if proxies[0] != global || proxies[1] != global {
		t.Fatalf("global proxy not used: %v", proxies)
	}
	mu.Unlock()
	if r.Attempts[0].ErrorCode != "RSYNC_PROXY" || strings.Contains(r.Attempts[0].Error, "global-secret") {
		t.Fatalf("proxy failure not recorded or not redacted: %+v", r.Attempts[0])
	}
	log, _, err := e.Log(r.ID, 1, 0)
	if err != nil || strings.Contains(log, "global-secret") || !strings.Contains(log, "[REDACTED]") {
		t.Fatal("rsync proxy credentials reached the log", log, err)
	}
	var saved Run
	if err = store.Run(r.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Snapshot.Settings.Rsync.Proxy != "" || saved.Snapshot.Source.Rsync.Proxy != "" {
		t.Fatal("proxy stored in run snapshot")
	}

	for _, mode := range []struct{ mode, proxy, want string }{
		{config.ProxyCustom, custom, custom},
		{config.ProxyDirect, "", ""},
		{config.ProxyGlobal, "", global},
	} {
		update(mode.mode, mode.proxy)
		r = run()
		mu.Lock()
		got := proxies[len(proxies)-1]
		mu.Unlock()
		if got != mode.want {
			t.Fatalf("mode %s used proxy %q", mode.mode, got)
		}
		if err = store.Run(r.ID, &saved); err != nil || saved.Snapshot.Source.Rsync.Proxy != "" || saved.Snapshot.Source.Rsync.ProxyMode != mode.mode {
			t.Fatal("snapshot must keep the mode without the URL", saved.Snapshot.Source.Rsync, err)
		}
	}
}
