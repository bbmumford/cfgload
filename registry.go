package cfgload

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
)

// registryEntry holds a single config snapshot and its subscribers.
type registryEntry struct {
	v    atomic.Value // stores a pointer to a concrete config type
	typ  reflect.Type // concrete type (usually a pointer to struct)
	mu   sync.Mutex   // protects subscribers and type init
	subs map[int]*subscriber
	next int
}

// subscriber delivers one callback's snapshots in the order UpdateValue stored
// them, one at a time, off the updating goroutine: UpdateValue queues each
// snapshot and at most one goroutine per subscriber drains the queue, so a
// slow callback delays only its own later deliveries and a later snapshot
// never reaches a callback before an earlier one.
type subscriber struct {
	fn      func(interface{})
	mu      sync.Mutex
	queue   []interface{}
	running bool
	stopped bool
}

// deliver queues snap and starts the drain when none is running.
func (s *subscriber) deliver(snap interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.queue = append(s.queue, snap)
	if !s.running {
		s.running = true
		go s.drain()
	}
}

// drain calls fn with each queued snapshot in order until the queue is empty
// or the subscriber is stopped. A panicking callback is recovered so one bad
// subscriber cannot end the process, and the next snapshot is still
// delivered.
func (s *subscriber) drain() {
	for {
		s.mu.Lock()
		if s.stopped || len(s.queue) == 0 {
			s.running = false
			s.queue = nil
			s.mu.Unlock()
			return
		}
		snap := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		func() {
			defer func() { _ = recover() }()
			s.fn(snap)
		}()
	}
}

// stop ends delivery: a snapshot still queued is dropped.
func (s *subscriber) stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

var (
	regMu sync.RWMutex
	reg   = make(map[string]*registryEntry)
)

// keyFor returns the explicit name if provided, otherwise a default key
// derived from the target's concrete type (package path + type name).
func keyFor(target interface{}, explicit string) string {
	if explicit != "" {
		return explicit
	}
	t := reflect.TypeOf(target)
	if t == nil {
		return "<nil>"
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.PkgPath() == "" {
		return t.String()
	}
	return t.PkgPath() + "." + t.Name()
}

func getOrCreateEntry(name string) *registryEntry {
	regMu.RLock()
	e := reg[name]
	regMu.RUnlock()
	if e != nil {
		return e
	}
	regMu.Lock()
	defer regMu.Unlock()
	if e = reg[name]; e == nil {
		e = &registryEntry{subs: make(map[int]*subscriber)}
		reg[name] = e
	}
	return e
}

// cloneSnapshot makes a deep copy of v into a new value of the same concrete type
// so readers see an immutable snapshot. JSON roundtrip is used for simplicity and
// type-agnostic copying. The stored value is a pointer when the source is a pointer.
func cloneSnapshot(v interface{}) (interface{}, error) {
	if v == nil {
		return nil, nil
	}
	t := reflect.TypeOf(v)
	// allocate a fresh value of the same type shape
	var dstVal reflect.Value
	if t.Kind() == reflect.Ptr {
		dstVal = reflect.New(t.Elem())
	} else {
		dstVal = reflect.New(t)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, dstVal.Interface()); err != nil {
		return nil, err
	}
	if t.Kind() == reflect.Ptr {
		return dstVal.Interface(), nil
	}
	return dstVal.Elem().Interface(), nil
}

// UpdateValue registers or updates the snapshot under the given name.
// It enforces a stable concrete type across updates.
func UpdateValue(name string, value interface{}) error {
	if name == "" {
		return fmt.Errorf("registry name cannot be empty")
	}
	e := getOrCreateEntry(name)
	snap, err := cloneSnapshot(value)
	if err != nil {
		return fmt.Errorf("failed to snapshot value: %w", err)
	}
	incomingType := reflect.TypeOf(snap)

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.typ == nil {
		e.typ = incomingType
	} else if e.typ != incomingType {
		return fmt.Errorf("type mismatch for key %q: have %v, got %v", name, e.typ, incomingType)
	}

	e.v.Store(snap)

	// Queue the snapshot on every subscriber while e.mu is held, so two
	// updates reach each subscriber in the order they were stored.
	for _, s := range e.subs {
		s.deliver(snap)
	}
	return nil
}

// Get returns the stored snapshot as interface{}.
func Get(name string) (interface{}, bool) {
	regMu.RLock()
	e := reg[name]
	regMu.RUnlock()
	if e == nil {
		return nil, false
	}
	v := e.v.Load()
	if v == nil {
		return nil, false
	}
	return v, true
}

// GetAs retrieves a snapshot and asserts it to the desired type.
func GetAs[T any](name string) (T, bool) {
	var zero T
	v, ok := Get(name)
	if !ok {
		return zero, false
	}
	vv, ok := v.(T)
	return vv, ok
}

// Exists checks if a configuration with the given name exists in the registry.
func Exists(name string) bool {
	regMu.RLock()
	_, ok := reg[name]
	regMu.RUnlock()
	return ok
}

// MustGetAs retrieves a snapshot and panics if missing or of wrong type.
func MustGetAs[T any](name string) T {
	v, ok := GetAs[T](name)
	if !ok {
		panic(fmt.Sprintf("config %q not found or wrong type", name))
	}
	return v
}

// Subscribe registers a callback that is invoked with the new snapshot
// whenever the value under name is updated, on a goroutine of its own and in
// the order the updates were stored. Returns an unsubscribe function, after
// which no further snapshot is delivered.
func Subscribe(name string, fn func(interface{})) (func(), error) {
	if name == "" || fn == nil {
		return func() {}, fmt.Errorf("invalid subscribe args")
	}
	e := getOrCreateEntry(name)
	e.mu.Lock()
	id := e.next
	e.next++
	s := &subscriber{fn: fn}
	e.subs[id] = s
	e.mu.Unlock()

	return func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
		s.stop()
	}, nil
}

// SubscribeAs is a typed helper around Subscribe.
func SubscribeAs[T any](name string, fn func(T)) (func(), error) {
	if fn == nil {
		return func() {}, fmt.Errorf("nil callback")
	}
	return Subscribe(name, func(v interface{}) {
		if vv, ok := v.(T); ok {
			fn(vv)
		}
	})
}

// Register is a convenience wrapper around UpdateValue for registering
// primitive values (strings, ints, etc.) or simple structs in the registry.
// Use this for shared platform values like cookie names, domains, etc.
func Register(name string, value interface{}) error {
	return UpdateValue(name, value)
}
