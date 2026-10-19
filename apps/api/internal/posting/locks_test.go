package posting

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

var (
	idA = uuid.MustParse("00000000-0000-7000-8000-00000000000a")
	idB = uuid.MustParse("00000000-0000-7000-8000-00000000000b")
	idC = uuid.MustParse("ffffffff-0000-7000-8000-000000000000")
)

func TestLockSetOrdered(t *testing.T) {
	var s LockSet
	s.Add(LockFundAccounts, idB)
	s.Add(LockSkuStock, idC, idA, idC)
	s.Add(LockPayments)
	s.Add(LockSkuStock, idB, idA)
	got, err := s.Ordered()
	if err != nil {
		t.Fatal(err)
	}
	want := []LockStep{
		{Kind: LockSkuStock, IDs: []uuid.UUID{idA, idB, idC}},
		{Kind: LockFundAccounts, IDs: []uuid.UUID{idB}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Ordered() = %v, want %v", got, want)
	}

	var empty LockSet
	if steps, err := empty.Ordered(); err != nil || len(steps) != 0 {
		t.Fatalf("empty set = %v, %v", steps, err)
	}

	s.Add("warehouses", idA)
	if _, err := s.Ordered(); err == nil || !strings.Contains(err.Error(), "warehouses") {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestLockerAcquireFollowsGlobalOrder(t *testing.T) {
	l := NewLocker()
	var calls []string
	record := func(kind string) LockFunc {
		return func(_ context.Context, _ db.Querier, ids []uuid.UUID) error {
			calls = append(calls, kind+":"+ids[0].String()[:8])
			return nil
		}
	}
	for _, kind := range lockOrder {
		l.Register(kind, record(kind))
	}
	var s LockSet
	for i := len(lockOrder) - 1; i >= 0; i-- {
		s.Add(lockOrder[i], idA)
	}
	if err := l.Acquire(context.Background(), nil, s); err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(lockOrder))
	for i, kind := range lockOrder {
		want[i] = kind + ":00000000"
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("lock calls %v, want %v", calls, want)
	}
}

func TestLockerAcquireErrors(t *testing.T) {
	l := NewLocker()
	boom := errors.New("boom")
	l.Register(LockLots, func(context.Context, db.Querier, []uuid.UUID) error { return boom })

	var unregistered LockSet
	unregistered.Add(LockSkuStock, idA)
	if err := l.Acquire(context.Background(), nil, unregistered); err == nil || !strings.Contains(err.Error(), "no locker registered") {
		t.Fatalf("unregistered kind: %v", err)
	}
	var failing LockSet
	failing.Add(LockLots, idA)
	if err := l.Acquire(context.Background(), nil, failing); !errors.Is(err, boom) {
		t.Fatalf("lock failure: %v", err)
	}
	var unknown LockSet
	unknown.Add("nope", idA)
	if err := l.Acquire(context.Background(), nil, unknown); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if err := l.Acquire(context.Background(), nil, LockSet{}); err != nil {
		t.Fatalf("empty set: %v", err)
	}
}

func TestLockerRegisterPanics(t *testing.T) {
	expectPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		fn()
	}
	noop := func(context.Context, db.Querier, []uuid.UUID) error { return nil }
	expectPanic("unknown kind", func() { NewLocker().Register("nope", noop) })
	expectPanic("duplicate", func() {
		l := NewLocker()
		l.Register(LockLots, noop)
		l.Register(LockLots, noop)
	})
}
