package notification

import (
	"context"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

type kindsOnly []string

func (k kindsOnly) Kinds() []string { return k }

func (kindsOnly) Sweep(context.Context, db.Querier, SweepTime) ([]Notice, error) { return nil, nil }

func TestDefaultProducersCoverEveryKindOnce(t *testing.T) {
	r := DefaultProducers()
	for _, k := range []string{KindReceivableOverdue, KindPayableDueSoon, KindLotExpiring, KindLotExpired, KindBelowMinStock,
		KindSurplusStock, KindPOSSyncException, KindStatementDisputed, KindSubscriptionStatus, KindExportReady} {
		if !r.kinds[k] {
			t.Errorf("no producer raises %s", k)
		}
	}
}

func TestRegisteringAKindTwicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a second producer of below_min_stock was accepted")
		}
	}()
	NewProducerRegistry(StockThresholds{}, kindsOnly{KindBelowMinStock})
}

func TestStates(t *testing.T) {
	for _, s := range []string{StateActive, StateUnread, StateSnoozed, StateResolved, StateAll} {
		if !validState(s) {
			t.Errorf("%s rejected", s)
		}
	}
	if validState("archived") {
		t.Error("unknown state accepted")
	}
	if stateFilter(StateActive) != stateFilter("") {
		t.Error("the default filter is not the active one")
	}
}
