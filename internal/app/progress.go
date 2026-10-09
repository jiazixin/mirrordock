package app

import (
	"context"
	"sync"
	"time"
)

// progressFrames retains one immutable frame for at most one second. Concurrent
// SSE clients share both the database query and JSON encoding. No background work
// is started when nobody is listening, and a disconnected waiter can leave early.
type progressFrames struct {
	mu      sync.Mutex
	data    []byte
	expires time.Time
	pending chan struct{}
}

func (p *progressFrames) get(ctx context.Context, read func(context.Context) ([]byte, error)) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if time.Now().Before(p.expires) {
			data := p.data
			p.mu.Unlock()
			return data, nil
		}
		if pending := p.pending; pending != nil {
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		p.pending = make(chan struct{})
		p.mu.Unlock()

		data, err := read(ctx)
		p.mu.Lock()
		if err == nil {
			p.data = data
			p.expires = time.Now().Add(time.Second)
		}
		close(p.pending)
		p.pending = nil
		p.mu.Unlock()
		return data, err
	}
}
