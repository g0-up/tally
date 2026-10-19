package org

import (
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
)

func TestOnboardingStepStatusAndSkippable(t *testing.T) {
	now := time.Now()
	tests := []struct {
		step OnboardingStep
		want string
	}{
		{OnboardingStep{Key: StepStoreProfile}, StepPending},
		{OnboardingStep{Key: StepCatalogSetup, SkippedAt: &now}, StepSkipped},
		{OnboardingStep{Key: StepCatalogSetup, SkippedAt: &now, CompletedAt: &now}, StepCompleted},
	}
	for _, tc := range tests {
		if got := tc.step.Status(); got != tc.want {
			t.Errorf("%+v: Status = %s, want %s", tc.step, got, tc.want)
		}
	}
	for _, key := range OnboardingSteps {
		want := key == StepCatalogSetup || key == StepFirstSale
		if got := (OnboardingStep{Key: key}).Skippable(); got != want {
			t.Errorf("%s: Skippable = %v, want %v", key, got, want)
		}
	}
	if OnboardingSteps[len(OnboardingSteps)-1] != StepConfirmOpenings {
		t.Errorf("the opening confirmation must be the last step: %v", OnboardingSteps)
	}
	for _, key := range openingSteps {
		if stepOrder(key) < 0 || stepOrder(key) > stepOrder(StepConfirmOpenings) {
			t.Errorf("opening step %s must come before the confirmation", key)
		}
	}
}

func TestOnboardingStepsRefuseManualChangesTheyDoNotAllow(t *testing.T) {
	s := &Service{}
	actor := audit.Actor{Type: audit.ActorOwner}
	tests := []struct {
		key  string
		code string
	}{
		{StepConfirmOpenings, "confirmation_required"},
		{StepFirstSale, "step_not_manual"},
		{"payroll", "not_found"},
	}
	for _, tc := range tests {
		if _, err := s.CompleteStep(t.Context(), actor, tc.key); !apperr.Is(err, tc.code) {
			t.Errorf("CompleteStep(%s) = %v, want %s", tc.key, err, tc.code)
		}
	}
	for _, key := range []string{StepStoreProfile, StepOpeningStock, StepConfirmOpenings} {
		if _, err := s.SkipStep(t.Context(), actor, key); !apperr.Is(err, "step_not_skippable") {
			t.Errorf("SkipStep(%s) = %v, want step_not_skippable", key, err)
		}
	}
}
