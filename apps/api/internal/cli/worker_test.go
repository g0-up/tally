package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEachTenantContinuesPastFailures(t *testing.T) {
	tenants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	var visited []uuid.UUID
	err := eachTenant(context.Background(), tenants, func(id uuid.UUID) error {
		visited = append(visited, id)
		if id == tenants[1] {
			return errors.New("boom")
		}
		return nil
	})
	if len(visited) != 3 {
		t.Fatalf("visited %d tenants, want 3", len(visited))
	}
	if err == nil || !strings.Contains(err.Error(), tenants[1].String()) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the failing tenant named", err)
	}
	if err := eachTenant(context.Background(), tenants, func(uuid.UUID) error { return nil }); err != nil {
		t.Fatalf("no failures: %v", err)
	}
}

func TestEachTenantStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := eachTenant(ctx, []uuid.UUID{uuid.New(), uuid.New()}, func(uuid.UUID) error {
		calls++
		cancel()
		return nil
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls = %d, err = %v", calls, err)
	}
}
