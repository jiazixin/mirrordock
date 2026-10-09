package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressFramesShareConcurrentReadAndExpire(t *testing.T) {
	var frames progressFrames
	var calls atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	read := func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return []byte("snapshot"), nil
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			data, err := frames.get(t.Context(), read)
			if err != nil || string(data) != "snapshot" {
				t.Errorf("frame %s: %v", data, err)
			}
		})
	}
	<-entered
	close(release)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicated concurrent query", calls.Load())
	}
	frames.mu.Lock()
	frames.expires = time.Time{}
	frames.mu.Unlock()
	if _, err := frames.get(t.Context(), read); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("expired frame not refreshed", calls.Load())
	}
}

func TestProgressFramesWaiterCancellationAndFailureRecovery(t *testing.T) {
	var frames progressFrames
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	failure := errors.New("read failed")
	go func() {
		defer close(done)
		_, err := frames.get(t.Context(), func(context.Context) ([]byte, error) { close(entered); <-release; return nil, failure })
		if !errors.Is(err, failure) {
			t.Errorf("missing failure: %v", err)
		}
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := frames.get(ctx, func(context.Context) ([]byte, error) { t.Error("waiter started a duplicate read"); return nil, nil })
	close(release)
	<-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	data, err := frames.get(t.Context(), func(context.Context) ([]byte, error) { return []byte("recovered"), nil })
	if err != nil || string(data) != "recovered" {
		t.Fatal(string(data), err)
	}
}
