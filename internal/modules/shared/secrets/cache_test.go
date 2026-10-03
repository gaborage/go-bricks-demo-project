package secrets

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testValue2 = "value2"

// expireNow backdates key's entry so it is already expired. Tests use it on a
// long-TTL cache instead of sleeping past a short TTL: the background cleanup
// loop never fires during the test, and a slow CI runner cannot let a "fresh"
// entry expire before it is read.
func expireNow(t *testing.T, c *Cache, key string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		t.Fatalf("expireNow: key %q not in cache", key)
	}
	entry.ExpiresAt = time.Now().Add(-time.Second)
}

func TestCacheEntry_IsExpired(t *testing.T) {
	tests := []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{"future", time.Now().Add(time.Hour), false},
		{"past", time.Now().Add(-time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &CacheEntry{Value: "v", ExpiresAt: tt.expires}
			if got := entry.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCacheMetrics_HitRate(t *testing.T) {
	tests := []struct {
		name string
		m    CacheMetrics
		want float64
	}{
		{"no_reads", CacheMetrics{}, 0.0},
		{"all_hits", CacheMetrics{Hits: 10, TotalReads: 10}, 100.0},
		{"all_misses", CacheMetrics{Misses: 10, TotalReads: 10}, 0.0},
		{"half_hits", CacheMetrics{Hits: 5, Misses: 5, TotalReads: 10}, 50.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.HitRate(); got != tt.want {
				t.Errorf("HitRate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_SetGet(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")

	got := c.Get("key1")
	if got != "value1" {
		t.Errorf("Get() = %v, want value1", got)
	}
}

func TestCache_GetMissingKey(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	if got := c.Get("missing"); got != nil {
		t.Errorf("Get() = %v, want nil", got)
	}
}

func TestCache_GetOverwritesExisting(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")
	c.Set("key1", testValue2)

	if got := c.Get("key1"); got != testValue2 {
		t.Errorf("Get() = %v, want value2", got)
	}
	if size := c.Size(); size != 1 {
		t.Errorf("Size() = %d, want 1", size)
	}
}

func TestCache_Delete(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")
	c.Delete("key1")

	if got := c.Get("key1"); got != nil {
		t.Errorf("Get() after Delete() = %v, want nil", got)
	}
	if size := c.Size(); size != 0 {
		t.Errorf("Size() = %d, want 0", size)
	}
}

func TestCache_DeleteMissingKeyNoPanic(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Delete("does-not-exist")
}

func TestCache_Clear(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")
	c.Set("key2", testValue2)
	c.Clear()

	if size := c.Size(); size != 0 {
		t.Errorf("Size() after Clear() = %d, want 0", size)
	}
	if got := c.Get("key1"); got != nil {
		t.Errorf("Get() after Clear() = %v, want nil", got)
	}
}

func TestCache_Size(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	if size := c.Size(); size != 0 {
		t.Errorf("Size() = %d, want 0", size)
	}

	c.Set("key1", "value1")
	c.Set("key2", testValue2)

	if size := c.Size(); size != 2 {
		t.Errorf("Size() = %d, want 2", size)
	}
}

func TestCache_Expiry(t *testing.T) {
	const ttl = time.Hour
	c := NewCache(ttl, 10)
	defer c.Close()

	before := time.Now()
	c.Set("key1", "value1")

	c.mu.RLock()
	expiresAt := c.entries["key1"].ExpiresAt
	c.mu.RUnlock()
	if expiresAt.Before(before.Add(ttl)) || expiresAt.After(time.Now().Add(ttl)) {
		t.Errorf("ExpiresAt = %v, want Set time + %v", expiresAt, ttl)
	}

	if got := c.Get("key1"); got != "value1" {
		t.Errorf("Get() before expiry = %v, want value1", got)
	}

	expireNow(t, c, "key1")

	if got := c.Get("key1"); got != nil {
		t.Errorf("Get() after expiry = %v, want nil", got)
	}
}

func TestCache_HitMissMetrics(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")

	c.Get("key1")    // hit
	c.Get("key1")    // hit
	c.Get("missing") // miss

	metrics := c.Metrics()
	if metrics.Hits != 2 {
		t.Errorf("Hits = %d, want 2", metrics.Hits)
	}
	if metrics.Misses != 1 {
		t.Errorf("Misses = %d, want 1", metrics.Misses)
	}
	if metrics.TotalReads != 3 {
		t.Errorf("TotalReads = %d, want 3", metrics.TotalReads)
	}
	wantRate := float64(2) / float64(3) * 100.0
	if metrics.HitRate() != wantRate {
		t.Errorf("HitRate() = %v, want %v", metrics.HitRate(), wantRate)
	}
}

func TestCache_ExpiredEntryCountsAsMiss(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")
	expireNow(t, c, "key1")

	c.Get("key1")

	metrics := c.Metrics()
	if metrics.Misses != 1 {
		t.Errorf("Misses = %d, want 1", metrics.Misses)
	}
	if metrics.Hits != 0 {
		t.Errorf("Hits = %d, want 0", metrics.Hits)
	}
}

func TestCache_MetricsTotalSize(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	c.Set("key1", "value1")
	c.Set("key2", testValue2)

	metrics := c.Metrics()
	if metrics.TotalSize != 2 {
		t.Errorf("TotalSize = %d, want 2", metrics.TotalSize)
	}

	c.Delete("key1")
	metrics = c.Metrics()
	if metrics.TotalSize != 1 {
		t.Errorf("TotalSize after Delete = %d, want 1", metrics.TotalSize)
	}
}

func TestCache_EvictOldestEntryAtCapacity(t *testing.T) {
	c := NewCache(time.Hour, 2)
	defer c.Close()

	c.Set("key1", "value1")
	c.Set("key2", testValue2)
	// At capacity: no expired entries to reclaim, so the oldest (key1,
	// inserted first and therefore expiring first) must be evicted.
	c.Set("key3", "value3")

	if size := c.Size(); size != 2 {
		t.Fatalf("Size() = %d, want 2", size)
	}
	if got := c.Get("key1"); got != nil {
		t.Errorf("Get(key1) = %v, want nil (should have been evicted)", got)
	}
	if got := c.Get("key2"); got != testValue2 {
		t.Errorf("Get(key2) = %v, want value2", got)
	}
	if got := c.Get("key3"); got != "value3" {
		t.Errorf("Get(key3) = %v, want value3", got)
	}

	metrics := c.Metrics()
	if metrics.Evictions != 1 {
		t.Errorf("Evictions = %d, want 1", metrics.Evictions)
	}
}

func TestCache_EvictExpiredEntriesBeforeOldest(t *testing.T) {
	c := NewCache(time.Hour, 2)
	defer c.Close()

	c.Set("key1", "value1")
	c.Set("key2", testValue2)
	expireNow(t, c, "key1")

	// The cache is at capacity, so Set() must reclaim space. key1 is expired,
	// so evictExpiredEntries removes it and evictOldestEntry never runs,
	// leaving key2 intact.
	c.Set("key3", "value3")

	if evictions := c.Metrics().Evictions; evictions != 1 {
		t.Errorf("Evictions = %d, want 1 (only the expired entry)", evictions)
	}

	if got := c.Get("key1"); got != nil {
		t.Errorf("Get(key1) = %v, want nil (expired entry should be gone)", got)
	}
	if got := c.Get("key2"); got != testValue2 {
		t.Errorf("Get(key2) = %v, want value2 (should survive expired-entry eviction)", got)
	}
	if got := c.Get("key3"); got != "value3" {
		t.Errorf("Get(key3) = %v, want value3", got)
	}
}

func TestCache_CleanupLoopRemovesExpiredEntriesInBackground(t *testing.T) {
	c := NewCache(20*time.Millisecond, 10)
	defer c.Close()

	c.Set("key1", "value1")

	// cleanupLoop ticks every ttl/2 (10ms here); give it enough cycles to
	// run after the entry has expired, without calling Get/cleanup directly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		size := len(c.entries)
		c.mu.RUnlock()
		if size == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cleanupLoop did not remove expired entry in background")
}

func TestCache_ConcurrentSet(t *testing.T) {
	c := NewCache(time.Hour, 1000)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Set(fmt.Sprintf("key-%d", n), n)
		}(i)
	}
	wg.Wait()

	if size := c.Size(); size != 50 {
		t.Errorf("Size() = %d, want 50", size)
	}
}

// TestCache_ConcurrentGet guards the lock Get() takes. Get() updates the
// hit/miss counters, so under a read lock concurrent readers race on them:
// `go test -race` flags it, and increments are lost (TotalReads < 50).
func TestCache_ConcurrentGet(t *testing.T) {
	c := NewCache(time.Hour, 1000)
	defer c.Close()
	c.Set("shared-key", "value")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Get("shared-key")
		}()
	}
	wg.Wait()

	if reads := c.Metrics().TotalReads; reads != 50 {
		t.Errorf("TotalReads = %d, want 50", reads)
	}
}

func TestCache_ConcurrentSetSameKey(t *testing.T) {
	c := NewCache(time.Hour, 10)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Set("shared-key", n)
		}(i)
	}
	wg.Wait()

	if size := c.Size(); size != 1 {
		t.Errorf("Size() = %d, want 1", size)
	}
}

func TestCache_ConcurrentSetAndDelete(t *testing.T) {
	c := NewCache(time.Hour, 1000)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			c.Set(fmt.Sprintf("key-%d", n), n)
		}(i)
		go func(n int) {
			defer wg.Done()
			c.Delete(fmt.Sprintf("key-%d", n))
		}(i)
	}
	wg.Wait()
}

func TestCache_Close(t *testing.T) {
	// Counted before NewCache, so the cleanup goroutine it starts is excluded
	// and the loop below waits for that goroutine to exit.
	before := runtime.NumGoroutine()

	c := NewCache(10*time.Millisecond, 10)
	c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("cleanup goroutine still running after Close(): before=%d, now=%d", before, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCache_CloseTwiceDoesNotPanic(t *testing.T) {
	c := NewCache(time.Hour, 10)

	c.Close()
	c.Close()
}

func TestCache_CloseConcurrentDoesNotPanic(t *testing.T) {
	c := NewCache(time.Hour, 10)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()
}
