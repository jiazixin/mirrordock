package mirrors

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRootCacheBoundsLeasesAndCloses(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	c, err := newRootCache(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.close() })
	a, releaseA, err := c.borrow("a")
	if err != nil {
		t.Fatal(err)
	}
	b, releaseB, err := c.borrow("b")
	if err != nil {
		t.Fatal(err)
	}
	transient, releaseTransient, err := c.borrow("c")
	if err != nil || len(c.items) != 2 {
		t.Fatal("all borrowed roots must remain bounded", err, len(c.items))
	}
	releaseTransient()
	if _, err := transient.Stat("."); !errors.Is(err, os.ErrClosed) {
		t.Fatal("transient root leaked", err)
	}
	releaseA()
	again, releaseAgain, err := c.borrow("a")
	if err != nil || again != a {
		t.Fatal("hot root not reused", err)
	}
	releaseAgain()
	third, releaseThird, err := c.borrow("c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Stat("."); !errors.Is(err, os.ErrClosed) {
		t.Fatal("idle eviction leaked", err)
	}
	c.forget("c")
	if _, err := third.Stat("."); err != nil {
		t.Fatal("forget closed borrowed root", err)
	}
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat("."); err != nil {
		t.Fatal("close broke active lease", err)
	}
	releaseB()
	releaseThird()
	for _, root := range []*os.Root{b, third} {
		if _, err := root.Stat("."); !errors.Is(err, os.ErrClosed) {
			t.Fatal("released root leaked after shutdown", err)
		}
	}
	if _, _, err := c.borrow("a"); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed cache accepted new lease", err)
	}
}

func TestRootCacheConcurrentAndConfined(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	c, err := newRootCache(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if root, release, err := c.borrow("escape"); err == nil {
		release()
		t.Fatal("external root opened", root)
	}
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			for range 100 {
				root, release, err := c.borrow("safe")
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := root.Stat("."); err != nil {
					t.Error(err)
				}
				release()
			}
		})
	}
	wg.Wait()
}
