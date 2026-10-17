package app

import (
	"context"
	"sync"
	"time"
)

// progressFrames retains one immutable frame for at most one second. Concurrent
// SSE clients share both the database query and JSON encoding. No background work
// is started when nobody is listening, and a disconnected waiter can leave early.
// invalidate drops the frame after a management write; a read that overlapped the
// write is discarded and repeated, so no client is served state from before it.
type progressFrames struct {
	mu         sync.Mutex
	data       []byte
	expires    time.Time
	pending    chan struct{}
	generation uint64
	changed    chan struct{}
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
		generation := p.generation
		p.mu.Unlock()

		data, err := read(ctx)
		p.mu.Lock()
		stale := p.generation != generation
		if err == nil && !stale {
			p.data = data
			p.expires = time.Now().Add(time.Second)
		}
		close(p.pending)
		p.pending = nil
		p.mu.Unlock()
		if stale && err == nil {
			continue
		}
		return data, err
	}
}

// invalidate expires the current frame and wakes listeners waiting on changes.
func (p *progressFrames) invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.expires = time.Time{}
	if p.changed != nil {
		close(p.changed)
		p.changed = nil
	}
}

// changes returns a channel closed by the next invalidate. Take it before reading
// a frame so an invalidation during the read is not missed.
func (p *progressFrames) changes() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changed == nil {
		p.changed = make(chan struct{})
	}
	return p.changed
}
