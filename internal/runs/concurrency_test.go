package runs

import (
	"context"
	"io"
	"mirrordock/internal/config"
	syncer "mirrordock/internal/sync"
	"testing"
	"time"
)

func TestConcurrencyIsPerSourceType(t *testing.T) {
	release := make(chan struct{})
	e, cfg, _, _ := testEngine(t, "10s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
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
	if err := e.ApplyConfig(c, view.Revision, nil, nil); err != nil {
		t.Fatal(err)
	}

	first, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, e, first.ID, "running")
	blocked, err := e.Enqueue("second", "manual")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.Enqueue("release", "manual")
	if err != nil {
		t.Fatal(err)
	}
	// A busy rsync slot must not hold back another type.
	waitState(t, e, other.ID, "running")
	time.Sleep(100 * time.Millisecond)
	if r, err := e.Get(blocked.ID); err != nil || r.State != "queued" {
		t.Fatal("second rsync run ignored the per-type limit", r, err)
	}

	view = cfg.View()
	c = view.Config
	c.Settings.Concurrency.Rsync = 2
	if err := e.ApplyConfig(c, view.Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, blocked.ID, "running")
	close(release)
}
