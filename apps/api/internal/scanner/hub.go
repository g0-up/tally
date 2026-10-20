package scanner

import (
	"errors"
	"sync"

	"github.com/google/uuid"
)

// Role is the side of a session a stream serves.
type Role string

// Roles.
const (
	RoleDesktop Role = "desktop"
	RolePhone   Role = "phone"
)

// Key names a session across tenants.
type Key struct {
	TenantID  uuid.UUID
	SessionID uuid.UUID
}

// Event is one server-sent event; Data is encoded as JSON.
type Event struct {
	Name string
	Data any
}

// Event names.
const (
	EventState          = "state"
	EventClaimRequested = "claim_requested"
	EventScan           = "scan"
	EventApproved       = "approved"
	EventRejected       = "rejected"
	EventRevoked        = "revoked"
	EventEnded          = "ended"
	EventReplaced       = "replaced"
	EventRestarting     = "restarting"
)

// Hub errors.
var (
	// ErrHubClosed means the process is shutting down; the client should reconnect later.
	ErrHubClosed = errors.New("scanner: hub closed")
	// ErrStreamLimit means the tenant or the process holds as many streams as it may.
	ErrStreamLimit = errors.New("scanner: stream limit reached")
)

// Hub moves events and scan results between the requests of one process. Every method is safe
// for concurrent use and never blocks on a slow reader.
type Hub interface {
	// Subscribe opens the stream of one side of a session, replacing the open one (which gets
	// replaced and is closed). cancel removes the stream only while it is still the current one,
	// so a handler that exits late after a reload cannot remove its successor.
	Subscribe(key Key, role Role) (events <-chan Event, cancel func(), err error)
	// Publish queues ev on the stream of one side without blocking; false means there is no
	// stream or its buffer is full.
	Publish(key Key, role Role, ev Event) bool
	// Await registers a waiter for the result of a scan of the session. The channel yields the
	// result once, or is closed when the session's streams close. cancel removes the waiter;
	// callers always defer it.
	Await(key Key, scanID uuid.UUID) (result <-chan ScanResult, cancel func())
	// Resolve hands r to every waiter of scanID and forgets them; without a waiter it does nothing.
	Resolve(scanID uuid.UUID, r ScanResult)
	// Close closes both streams of a session and its waiters.
	Close(key Key)
	// CloseAll sends restarting to every stream, closes every stream and waiter, and makes every
	// later Subscribe fail with ErrHubClosed. The server calls it when it shuts down.
	CloseAll()
}

// Stream limits of the in-memory hub.
const (
	defaultStreamsPerTenant = 20
	defaultStreamsTotal     = 2000
	// streamBuffer is how many events may wait on a stream. The channel holds one more slot that
	// only the terminal replaced or restarting event uses, so it always fits.
	streamBuffer = 16
)

type streamKey struct {
	key  Key
	role Role
}

type stream struct {
	token uint64
	ch    chan Event
}

type waiter struct {
	key Key
	ch  chan ScanResult
}

// MemoryHub is the Hub of a single API process. It keeps everything behind one mutex and only
// ever sends on a channel when the send cannot block.
type MemoryHub struct {
	maxPerTenant, maxTotal int

	mu        sync.Mutex
	closed    bool
	next      uint64
	streams   map[streamKey]*stream
	perTenant map[uuid.UUID]int
	waiters   map[uuid.UUID]map[uint64]waiter
}

var _ Hub = (*MemoryHub)(nil)

// NewMemoryHub returns a hub allowing 20 streams per tenant and 2000 in total.
func NewMemoryHub() *MemoryHub { return newMemoryHub(defaultStreamsPerTenant, defaultStreamsTotal) }

func newMemoryHub(perTenant, total int) *MemoryHub {
	return &MemoryHub{
		maxPerTenant: perTenant, maxTotal: total,
		streams: map[streamKey]*stream{}, perTenant: map[uuid.UUID]int{}, waiters: map[uuid.UUID]map[uint64]waiter{},
	}
}

// Subscribe implements Hub.
func (h *MemoryHub) Subscribe(key Key, role Role) (<-chan Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, ErrHubClosed
	}
	sk := streamKey{key: key, role: role}
	old := h.streams[sk]
	if old == nil {
		if h.perTenant[key.TenantID] >= h.maxPerTenant || len(h.streams) >= h.maxTotal {
			return nil, nil, ErrStreamLimit
		}
		h.perTenant[key.TenantID]++
	} else {
		terminate(old.ch, Event{Name: EventReplaced, Data: struct{}{}})
	}
	h.next++
	s := &stream{token: h.next, ch: make(chan Event, streamBuffer+1)}
	h.streams[sk] = s
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if cur := h.streams[sk]; cur != nil && cur.token == s.token {
			h.removeStream(sk, cur)
		}
	}
	return s.ch, cancel, nil
}

// Publish implements Hub.
func (h *MemoryHub) Publish(key Key, role Role, ev Event) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[streamKey{key: key, role: role}]
	if s == nil || len(s.ch) >= streamBuffer {
		return false
	}
	s.ch <- ev // cannot block: the channel has a free slot beyond streamBuffer
	return true
}

// Await implements Hub. After CloseAll it returns a closed channel.
func (h *MemoryHub) Await(key Key, scanID uuid.UUID) (<-chan ScanResult, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan ScanResult, 1)
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	h.next++
	token := h.next
	if h.waiters[scanID] == nil {
		h.waiters[scanID] = map[uint64]waiter{}
	}
	h.waiters[scanID][token] = waiter{key: key, ch: ch}
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if ws := h.waiters[scanID]; ws != nil {
			delete(ws, token)
			if len(ws) == 0 {
				delete(h.waiters, scanID)
			}
		}
	}
	return ch, cancel
}

// Resolve implements Hub.
func (h *MemoryHub) Resolve(scanID uuid.UUID, r ScanResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, w := range h.waiters[scanID] {
		select {
		case w.ch <- r:
		default: // already holds a result
		}
	}
	delete(h.waiters, scanID)
}

// Close implements Hub.
func (h *MemoryHub) Close(key Key) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, role := range []Role{RoleDesktop, RolePhone} {
		sk := streamKey{key: key, role: role}
		if s := h.streams[sk]; s != nil {
			h.removeStream(sk, s)
		}
	}
	for scanID, ws := range h.waiters {
		for token, w := range ws {
			if w.key == key {
				close(w.ch)
				delete(ws, token)
			}
		}
		if len(ws) == 0 {
			delete(h.waiters, scanID)
		}
	}
}

// CloseAll implements Hub.
func (h *MemoryHub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for sk, s := range h.streams {
		terminate(s.ch, Event{Name: EventRestarting, Data: struct{}{}})
		delete(h.streams, sk)
	}
	clear(h.perTenant)
	for _, ws := range h.waiters {
		for _, w := range ws {
			close(w.ch)
		}
	}
	clear(h.waiters)
}

// removeStream drops a stream from the maps and closes its channel; the caller holds the mutex.
func (h *MemoryHub) removeStream(sk streamKey, s *stream) {
	delete(h.streams, sk)
	if h.perTenant[sk.key.TenantID]--; h.perTenant[sk.key.TenantID] <= 0 {
		delete(h.perTenant, sk.key.TenantID)
	}
	close(s.ch)
}

// terminate queues the last event of a stream and closes it. The send cannot block: Publish
// leaves the slot beyond streamBuffer free for it.
func terminate(ch chan Event, ev Event) {
	select {
	case ch <- ev:
	default:
	}
	close(ch)
}
