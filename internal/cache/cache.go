// Package cache keeps named objects in memory for each resource.
// A database plugin stores its connection here. Mock stores a counter.
// The store is empty at startup. Shutdown drops every object.
package cache

import (
	"io"
	"reflect"
	"sort"
	"sync"
	"time"

	"gatekeep/internal/plugin"
)

// Object is the public description of one cached value.
// The value itself stays in memory.
type Object struct {
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
}

type entry struct {
	value   any
	created time.Time
	updated time.Time
}

// Store is the process-wide cache.
type Store struct {
	mu        sync.Mutex
	resources map[int64]map[string]*entry
}

func New() *Store {
	return &Store{resources: map[int64]map[string]*entry{}}
}

// Handle is the cache for one resource.
// idle is how long an object may sit unused before it is dropped.
// Using an object starts that wait over.
func (s *Store) Handle(resourceID int64, idle time.Duration) plugin.Cache {
	if idle <= 0 {
		idle = time.Nanosecond
	}
	return handle{store: s, resourceID: resourceID, idle: idle}
}

type handle struct {
	store      *Store
	resourceID int64
	idle       time.Duration
}

func (h handle) Update(name string, fn func(current any, ok bool) (next any, store bool)) (any, bool) {
	if !validName(name) {
		return nil, false
	}
	h.store.mu.Lock()
	var release []io.Closer
	defer func() {
		h.store.mu.Unlock()
		closeAll(release)
	}()

	now := time.Now()
	current, live := h.live(name, now, &release)
	next, store := fn(current, live)
	if !store {
		if !live {
			return nil, false
		}
		h.touch(name, now)
		return current, true
	}
	if live {
		release = append(release, replaced(current, next)...)
		h.resources()[name].value = next
		h.touch(name, now)
		return next, true
	}
	objects := h.resources()
	objects[name] = &entry{value: next, created: now, updated: now}
	return next, true
}

func (h handle) Drop(name string) {
	if !validName(name) {
		return
	}
	h.store.mu.Lock()
	var release []io.Closer
	defer func() {
		h.store.mu.Unlock()
		closeAll(release)
	}()
	objects := h.store.resources[h.resourceID]
	obj := objects[name]
	if obj == nil {
		return
	}
	release = append(release, closer(obj.value)...)
	delete(objects, name)
	if len(objects) == 0 {
		delete(h.store.resources, h.resourceID)
	}
}

func (h handle) live(name string, now time.Time, release *[]io.Closer) (any, bool) {
	objects := h.store.resources[h.resourceID]
	obj := objects[name]
	if obj == nil {
		return nil, false
	}
	if !now.Before(obj.updated.Add(h.idle)) {
		*release = append(*release, closer(obj.value)...)
		delete(objects, name)
		if len(objects) == 0 {
			delete(h.store.resources, h.resourceID)
		}
		return nil, false
	}
	return obj.value, true
}

func (h handle) resources() map[string]*entry {
	objects := h.store.resources[h.resourceID]
	if objects == nil {
		objects = map[string]*entry{}
		h.store.resources[h.resourceID] = objects
	}
	return objects
}

func (h handle) touch(name string, now time.Time) {
	if obj := h.store.resources[h.resourceID][name]; obj != nil {
		obj.updated = now
	}
}

// List returns the live objects for one resource and drops any that are idle.
func (s *Store) List(resourceID int64, idle time.Duration, now time.Time) []Object {
	s.mu.Lock()
	var release []io.Closer
	defer func() {
		s.mu.Unlock()
		closeAll(release)
	}()
	return s.collect(resourceID, idle, now, &release)
}

func (s *Store) collect(resourceID int64, idle time.Duration, now time.Time, release *[]io.Closer) []Object {
	objects := s.resources[resourceID]
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Object, 0, len(names))
	for _, name := range names {
		obj := objects[name]
		if idle <= 0 || !now.Before(obj.updated.Add(idle)) {
			*release = append(*release, closer(obj.value)...)
			delete(objects, name)
			continue
		}
		out = append(out, Object{
			Name:      name,
			CreatedAt: obj.created,
			UpdatedAt: obj.updated,
			ExpiresAt: obj.updated.Add(idle),
		})
	}
	if len(objects) == 0 {
		delete(s.resources, resourceID)
	}
	return out
}

// Expire drops objects that have been idle for the given duration.
func (s *Store) Expire(resourceID int64, idle time.Duration, now time.Time) {
	s.List(resourceID, idle, now)
}

// Clear drops one object. A missing name is left as it is.
func (s *Store) Clear(resourceID int64, name string) {
	s.Handle(resourceID, time.Hour).Drop(name)
}

// ClearResource drops every object for one resource.
func (s *Store) ClearResource(resourceID int64) {
	s.mu.Lock()
	var release []io.Closer
	defer func() {
		s.mu.Unlock()
		closeAll(release)
	}()
	for _, obj := range s.resources[resourceID] {
		release = append(release, closer(obj.value)...)
	}
	delete(s.resources, resourceID)
}

// ClearAll drops every resource cache. Shutdown calls this.
func (s *Store) ClearAll() {
	s.mu.Lock()
	var release []io.Closer
	defer func() {
		s.mu.Unlock()
		closeAll(release)
	}()
	for _, objects := range s.resources {
		for _, obj := range objects {
			release = append(release, closer(obj.value)...)
		}
	}
	s.resources = map[int64]map[string]*entry{}
}

// ResourceIDs lists resources that currently hold an object.
func (s *Store) ResourceIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.resources))
	for id := range s.resources {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r == '/' || r == ' ' {
			return false
		}
	}
	return true
}

func closer(value any) []io.Closer {
	item, ok := value.(io.Closer)
	if !ok || item == nil {
		return nil
	}
	return []io.Closer{item}
}

func replaced(current, next any) []io.Closer {
	item, ok := current.(io.Closer)
	if !ok || item == nil || same(current, next) {
		return nil
	}
	return []io.Closer{item}
}

func same(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left := reflect.ValueOf(a)
	right := reflect.ValueOf(b)
	if !left.IsValid() || !right.IsValid() || left.Type() != right.Type() || !left.Type().Comparable() {
		return false
	}
	return left.Interface() == right.Interface()
}

func closeAll(items []io.Closer) {
	for _, item := range items {
		_ = item.Close()
	}
}
