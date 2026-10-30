package run

import (
	"context"
	"testing"
	"time"
)

func shutdownEngine(t *testing.T, engine *Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	if err := engine.Shutdown(ctx); err != nil {
		t.Errorf("shutdown engine: %v", err)
	}
}
