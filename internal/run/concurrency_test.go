package run

import (
	"context"
	"io"
	"testing"

	"mirrordock/internal/config"
	"mirrordock/internal/syncer"
)

func TestConcurrencyIsPerSourceType(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e, cfg, _, _ := testEngine(testEngineOptions{t: t, timeout: "10s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	view := cfg.View()
	c := view.Config
	c.Settings.Concurrency = config.Concurrency{Rsync: 1, RepoSync: 1, GitHubRelease: 1}
	second := c.Sources[0]
	second.ID, second.Name, second.Dest = "second", "Second", "second"
	github := config.Source{
		ID:            "release",
		Name:          "Release",
		Type:          "github_release",
		Enabled:       true,
		Dest:          "release",
		GitHubRelease: config.GitHubRelease{Repository: "owner/repo", AssetPatterns: []string{"*"}},
		Upstreams:     map[string]string{},
	}
	c.Sources = append(c.Sources, second, github)
	if err := e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: c, Revision: view.Revision, Token: nil, Proxy: nil}); err != nil {
		t.Fatal(err)
	}

	first, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: first.ID, state: "running"})
	blocked, err := e.Enqueue(t.Context(), "second", "manual")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.Enqueue(t.Context(), "release", "manual")
	if err != nil {
		t.Fatal(err)
	}
	// A busy rsync slot must not hold back another type.
	waitState(waitStateOptions{t: t, e: e, id: other.ID, state: "running"})
	// Dispatch reached the later source; the blocked run must remain queued.
	if r, err := e.Get(t.Context(), blocked.ID); err != nil || r.State != "queued" {
		t.Fatal("second rsync run ignored the per-type limit", r, err)
	}

	view = cfg.View()
	c = view.Config
	c.Settings.Concurrency.Rsync = 2
	if err := e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: c, Revision: view.Revision, Token: nil, Proxy: nil}); err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: blocked.ID, state: "running"})
	close(release)
}
