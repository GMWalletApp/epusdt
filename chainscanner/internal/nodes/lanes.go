package nodes

import "sync"

type providerLanes struct {
	mu   sync.Mutex
	busy map[string]*providerUse
}
type providerUse struct {
	scans                int
	lookup, supplemental bool
}

func (l *providerLanes) use(key string) *providerUse {
	if l.busy == nil {
		l.busy = make(map[string]*providerUse)
	}
	if l.busy[key] == nil {
		l.busy[key] = new(providerUse)
	}
	return l.busy[key]
}

func (l *providerLanes) discardIdle(key string, v *providerUse) {
	if v.scans == 0 && !v.lookup && !v.supplemental {
		delete(l.busy, key)
	}
}

// Scanning and health probes always proceed, including while a strict lookup
// borrows a busy provider. Track active use only to inform query admission.
func (l *providerLanes) scan(key string) func() {
	if l == nil {
		return func() {}
	}
	l.mu.Lock()
	v := l.use(key)
	v.scans++
	l.mu.Unlock()
	return func() { l.mu.Lock(); defer l.mu.Unlock(); v.scans--; l.discardIdle(key, v) }
}

func (l *providerLanes) tryLookup(key string, allowBusy bool) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.busy[key]
	if v != nil && (!allowBusy || v.supplemental) {
		return nil, false
	}
	var slot *bool
	if v == nil {
		v = l.use(key)
		slot = &v.lookup
	} else {
		slot = &v.supplemental
	}
	*slot = true
	return func() { l.mu.Lock(); defer l.mu.Unlock(); *slot = false; l.discardIdle(key, v) }, true
}
