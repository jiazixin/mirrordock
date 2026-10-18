package app

import (
	"mirrordock/internal/mirrors"
	"net"
	"net/http"
	"sync"
	"time"
)

const stalledConnectionTimeout = 5 * time.Minute

type connection struct {
	conn       net.Conn
	active     bool
	bytes      uint64
	progressed time.Time
}

type connections struct {
	mu      sync.Mutex
	entries map[net.Conn]*connection
	limit   int
}

func connectionLimit() int {
	_, connections := mirrors.DescriptorLimits()
	return connections
}

func (c *connections) state(conn net.Conn, state http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch state {
	case http.StateNew:
		limit := c.limit
		if limit == 0 {
			limit = connectionLimit()
		}
		if len(c.entries) >= limit {
			_ = conn.Close()
			return
		}
		c.entries[conn] = &connection{conn: conn, progressed: time.Now()}
	case http.StateActive:
		if entry := c.entries[conn]; entry != nil {
			entry.active = true
			entry.progressed = time.Now()
			entry.bytes, _ = transmitted(conn)
		}
	case http.StateIdle:
		if entry := c.entries[conn]; entry != nil {
			entry.active = false
		}
	case http.StateHijacked, http.StateClosed:
		delete(c.entries, conn)
	}
}

func (c *connections) check(now time.Time) {
	c.mu.Lock()
	stalled := []net.Conn{}
	for _, entry := range c.entries {
		if !entry.active {
			continue
		}
		bytes, err := transmitted(entry.conn)
		if err != nil {
			continue
		}
		if bytes != entry.bytes {
			entry.bytes = bytes
			entry.progressed = now
		}
		if now.Sub(entry.progressed) >= stalledConnectionTimeout {
			stalled = append(stalled, entry.conn)
		}
	}
	c.mu.Unlock()
	for _, conn := range stalled {
		_ = conn.Close()
	}
}

func (a *App) watchConnections() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.closing:
			return
		case now := <-ticker.C:
			a.connections.check(now)
		}
	}
}
