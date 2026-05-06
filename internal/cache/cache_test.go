package cache

import (
	"testing"
	"time"
)

func TestCache_HitAndMiss(t *testing.T) {
	c := New[int](5 * time.Second)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	if _, ok := c.Get("k"); ok {
		t.Fatal("empty cache should miss")
	}
	c.Set("k", 42)
	if v, ok := c.Get("k"); !ok || v != 42 {
		t.Fatalf("expected hit (42, true), got (%d, %v)", v, ok)
	}
	if _, ok := c.Get("other"); ok {
		t.Fatal("unrelated key should miss")
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	c := New[string](5 * time.Second)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	c.Set("k", "v")
	now = now.Add(4 * time.Second)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("inside TTL should hit")
	}
	now = now.Add(2 * time.Second) // total 6s, > 5s TTL
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired entry should miss")
	}
}

func TestCache_DisabledWhenTTLZero(t *testing.T) {
	c := New[int](0)
	c.Set("k", 1)
	if _, ok := c.Get("k"); ok {
		t.Fatal("ttl=0 should disable cache")
	}
}
