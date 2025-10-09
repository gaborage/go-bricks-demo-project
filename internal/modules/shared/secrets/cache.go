package secrets

import (
	"sync"
	"time"
)

// CacheEntry represents a cached secret with expiration time
type CacheEntry struct {
	Value     any
	ExpiresAt time.Time
}

// IsExpired checks if the cache entry has expired
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// CacheMetrics tracks cache performance statistics
type CacheMetrics struct {
	Hits       int64
	Misses     int64
	Evictions  int64
	TotalReads int64
	TotalSize  int64
}

// HitRate calculates the cache hit rate as a percentage
func (m *CacheMetrics) HitRate() float64 {
	if m.TotalReads == 0 {
		return 0.0
	}
	return float64(m.Hits) / float64(m.TotalReads) * 100.0
}

// Cache provides thread-safe TTL-based caching with size limits and metrics
type Cache struct {
	entries map[string]*CacheEntry
	ttl     time.Duration
	maxSize int
	mu      sync.RWMutex
	metrics CacheMetrics
	stopCh  chan struct{}
	once    sync.Once
}

// NewCache creates a new cache with specified TTL and maximum size
func NewCache(ttl time.Duration, maxSize int) *Cache {
	cache := &Cache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
		stopCh:  make(chan struct{}),
	}

	// Start background cleanup goroutine
	go cache.cleanupLoop()

	return cache
}

// Get retrieves a value from the cache, returning nil if not found or expired
func (c *Cache) Get(key string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	c.metrics.TotalReads++

	entry, exists := c.entries[key]
	if !exists || entry.IsExpired() {
		c.metrics.Misses++
		return nil
	}

	c.metrics.Hits++
	return entry.Value
}

// Set stores a value in the cache with TTL expiration
func (c *Cache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict expired entries if we're at capacity
	if len(c.entries) >= c.maxSize {
		c.evictExpiredEntries()

		// If still at capacity, evict oldest entry
		if len(c.entries) >= c.maxSize {
			c.evictOldestEntry()
		}
	}

	c.entries[key] = &CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(c.ttl),
	}

	c.metrics.TotalSize = int64(len(c.entries))
}

// Delete removes a specific key from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
	c.metrics.TotalSize = int64(len(c.entries))
}

// Clear removes all entries from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
	c.metrics.TotalSize = 0
}

// Size returns the current number of entries in the cache
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Metrics returns a copy of the current cache metrics
func (c *Cache) Metrics() CacheMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metrics
}

// Close stops the background cleanup goroutine
func (c *Cache) Close() {
	c.once.Do(func() {
		close(c.stopCh)
	})
}

// cleanupLoop runs periodically to remove expired entries
func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(c.ttl / 2) // Clean up twice per TTL period
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCh:
			return
		}
	}
}

// cleanup removes expired entries from the cache
func (c *Cache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictExpiredEntries()
	c.metrics.TotalSize = int64(len(c.entries))
}

// evictExpiredEntries removes all expired entries (must be called with write lock)
func (c *Cache) evictExpiredEntries() {
	for key, entry := range c.entries {
		if entry.IsExpired() {
			delete(c.entries, key)
			c.metrics.Evictions++
		}
	}
}

// evictOldestEntry removes the oldest entry based on expiration time (must be called with write lock)
func (c *Cache) evictOldestEntry() {
	var oldestKey string
	var oldestTime time.Time

	// Find the entry with the earliest expiration time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.ExpiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.ExpiresAt
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
		c.metrics.Evictions++
	}
}
