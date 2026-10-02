package quotes

import "sync"

// cacheMap is a bounded map; when full it drops everything, which is simple
// and safe because entries are only an optimisation.
type cacheMap struct {
	mu      sync.Mutex
	max     int
	entries map[string][]byte
}

func newCacheMap(max int) *cacheMap {
	return &cacheMap{max: max, entries: map[string][]byte{}}
}

func (c *cacheMap) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.entries[key]
	return data, ok
}

func (c *cacheMap) put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		c.entries = map[string][]byte{}
	}
	c.entries[key] = data
}
