package mirrors

import (
	"container/list"
	"errors"
	"os"
	"sync"
)

// rootCache bounds idle and borrowed cached roots together. Extra borrowers
// use transient roots covered by the request descriptor gate.
type rootCache struct {
	mu     sync.Mutex
	anchor *os.Root
	limit  int
	items  map[string]*list.Element
	lru    list.List
	closed bool
}

type cachedRoot struct {
	path   string
	root   *os.Root
	refs   int
	cached bool
}

func newRootCache(path string, limit int) (*rootCache, error) {
	anchor, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &rootCache{anchor: anchor, limit: limit, items: make(map[string]*list.Element)}, nil
}

func (c *rootCache) borrow(path string) (*os.Root, func(), error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, os.ErrClosed
	}
	if element := c.items[path]; element != nil {
		entry := element.Value.(*cachedRoot)
		entry.refs++
		c.lru.MoveToFront(element)
		c.mu.Unlock()
		return entry.root, func() { c.release(entry) }, nil
	}
	c.mu.Unlock()
	// A cold filesystem lookup must not block leases of already cached roots.
	// os.Root supports concurrent OpenRoot and Close; check shutdown again after
	// opening, and deduplicate competing cold borrowers under the cache lock.
	root, err := c.anchor.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = root.Close()
		return nil, nil, os.ErrClosed
	}
	if element := c.items[path]; element != nil {
		_ = root.Close()
		entry := element.Value.(*cachedRoot)
		entry.refs++
		c.lru.MoveToFront(element)
		return entry.root, func() { c.release(entry) }, nil
	}
	if len(c.items) == c.limit {
		for element := c.lru.Back(); element != nil; element = element.Prev() {
			if entry := element.Value.(*cachedRoot); entry.refs == 0 {
				c.drop(element)
				break
			}
		}
	}
	entry := &cachedRoot{path: path, root: root, refs: 1, cached: len(c.items) < c.limit}
	if entry.cached {
		c.items[path] = c.lru.PushFront(entry)
	}
	return root, func() { c.release(entry) }, nil
}

func (c *rootCache) release(entry *cachedRoot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.refs--
	if entry.refs == 0 && !entry.cached {
		_ = entry.root.Close()
	}
}

func (c *rootCache) drop(element *list.Element) {
	entry := element.Value.(*cachedRoot)
	delete(c.items, entry.path)
	c.lru.Remove(element)
	entry.cached = false
	if entry.refs == 0 {
		_ = entry.root.Close()
	}
}

func (c *rootCache) forget(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.items[path]; element != nil {
		c.drop(element)
	}
}

func (c *rootCache) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var err error
	for element := c.lru.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(*cachedRoot)
		entry.cached = false
		if entry.refs == 0 {
			err = errors.Join(err, entry.root.Close())
		}
		element = previous
	}
	clear(c.items)
	c.lru.Init()
	return errors.Join(err, c.anchor.Close())
}
