package middleware

import (
	"container/list"
	"time"
)

// lru is a size-bounded map ordered by the last time each key was touched (oldest first). It is
// the memory bound of the in-process caches fed by untrusted input (host names, client
// addresses): inserting into a full lru evicts the least recently touched key instead of growing
// or refusing the insert. Not safe for concurrent use; callers hold their own lock.
type lru[V any] struct {
	max   int
	order *list.List // of *lruEntry[V], front = least recently touched
	items map[string]*list.Element
}

type lruEntry[V any] struct {
	key string
	val V
	at  time.Time
}

func newLRU[V any](maxEntries int) *lru[V] {
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &lru[V]{max: maxEntries, order: list.New(), items: map[string]*list.Element{}}
}

// get returns the entry of key without touching it.
func (l *lru[V]) get(key string) (*lruEntry[V], bool) {
	el, ok := l.items[key]
	if !ok {
		return nil, false
	}
	return entryOf[V](el), true
}

// touch marks key as used at time at; it reports false when key is absent.
func (l *lru[V]) touch(key string, at time.Time) bool {
	el, ok := l.items[key]
	if !ok {
		return false
	}
	entryOf[V](el).at = at
	l.order.MoveToBack(el)
	return true
}

// put stores val under key as used at time at, evicting the least recently touched keys while the
// lru is over its bound.
func (l *lru[V]) put(key string, val V, at time.Time) {
	if el, ok := l.items[key]; ok {
		e := entryOf[V](el)
		e.val, e.at = val, at
		l.order.MoveToBack(el)
		return
	}
	l.items[key] = l.order.PushBack(&lruEntry[V]{key: key, val: val, at: at})
	for len(l.items) > l.max {
		l.removeElement(l.order.Front())
	}
}

// remove deletes key.
func (l *lru[V]) remove(key string) {
	if el, ok := l.items[key]; ok {
		l.removeElement(el)
	}
}

// dropThrough deletes every key last touched at or before cutoff. Keys are ordered by touch time,
// so it stops at the first newer key.
func (l *lru[V]) dropThrough(cutoff time.Time) {
	for el := l.order.Front(); el != nil; el = l.order.Front() {
		if entryOf[V](el).at.After(cutoff) {
			return
		}
		l.removeElement(el)
	}
}

func (l *lru[V]) len() int { return len(l.items) }

func (l *lru[V]) removeElement(el *list.Element) {
	l.order.Remove(el)
	delete(l.items, entryOf[V](el).key)
}

// entryOf returns the entry of a list element; the list only ever holds *lruEntry[V].
func entryOf[V any](el *list.Element) *lruEntry[V] { return el.Value.(*lruEntry[V]) }
