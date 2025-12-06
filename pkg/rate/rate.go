package rate

import (
	"net"
	"sync"
	"time"
)

type entry struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
	updated      time.Time
}

type Attempts struct {
	mu           sync.RWMutex
	limit        int
	blockFor     time.Duration
	window       time.Duration
	cleanupEvery time.Duration
	data         map[string]*entry
	stopCh       chan struct{}
}

func NewAttempts(limit int, blockFor, window, cleanupEvery time.Duration) *Attempts {
	a := &Attempts{
		limit:        limit,
		blockFor:     blockFor,
		window:       window,
		cleanupEvery: cleanupEvery,
		data:         make(map[string]*entry),
		stopCh:       make(chan struct{}),
	}
	if cleanupEvery > 0 {
		go a.cleanupLoop()
	}
	return a
}

func (a *Attempts) cleanupLoop() {
	t := time.NewTicker(a.cleanupEvery)
	defer t.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case now := <-t.C:
			a.mu.Lock()
			for k, e := range a.data {
				if now.After(e.blockedUntil) && now.Sub(e.updated) > a.window {
					delete(a.data, k)
				}
			}
			a.mu.Unlock()
		}
	}
}

func (a *Attempts) Stop() { close(a.stopCh) }

func (a *Attempts) get(key string) (*entry, bool) {
	a.mu.RLock()
	e, ok := a.data[key]
	a.mu.RUnlock()
	return e, ok
}

func (a *Attempts) getOrCreate(key string) *entry {
	if e, ok := a.get(key); ok {
		return e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.data[key]; ok {
		return e
	}
	now := time.Now()
	e := &entry{windowStart: now, updated: now}
	a.data[key] = e
	return e
}

func (a *Attempts) IsBlocked(key string) bool {
	now := time.Now()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if e, ok := a.data[key]; ok {
		return now.Before(e.blockedUntil)
	}
	return false
}

func (a *Attempts) RemainingBlock(key string) time.Duration {
	now := time.Now()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if e, ok := a.data[key]; ok {
		if now.Before(e.blockedUntil) {
			return time.Until(e.blockedUntil)
		}
	}
	return 0
}

func (a *Attempts) Fail(key string) bool {
	now := time.Now()
	e := a.getOrCreate(key)

	a.mu.Lock()
	defer a.mu.Unlock()

	if now.Before(e.blockedUntil) {
		e.updated = now
		return false
	}

	if now.Sub(e.windowStart) > a.window {
		e.windowStart = now
		e.count = 0
	}

	e.count++
	e.updated = now

	if e.count >= a.limit {
		e.blockedUntil = now.Add(a.blockFor)
		return true
	}
	return false
}

func (a *Attempts) Success(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.data[key]; ok {
		e.count = 0
		e.windowStart = time.Now()
		e.updated = time.Now()
	}
}

func KeyFrom(ipStr, sess string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr
	}
	return ip.String()
}
