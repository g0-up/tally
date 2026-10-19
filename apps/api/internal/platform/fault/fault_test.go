package fault

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestHookIsInertOutsideTheTestEnvironment(t *testing.T) {
	for _, env := range []string{"production", "development", "", "TEST", "test "} {
		inj, err := Parse(env, "posting.before_commit:5")
		if err != nil {
			t.Fatalf("Parse(%q) = %v", env, err)
		}
		if inj != nil {
			t.Errorf("Parse(%q) armed the hook; it must be honoured only with APP_ENV=test", env)
		}
		if err := inj.Hit(PostingBeforeCommit, "pos.checkout"); err != nil {
			t.Errorf("APP_ENV=%q: Hit = %v, want nil", env, err)
		}
		if inj.Armed() {
			t.Errorf("APP_ENV=%q: Armed = true", env)
		}
	}
	// Even an invalid spec is ignored, not reported, outside the test environment.
	if inj, err := Parse("production", "nope:x"); inj != nil || err != nil {
		t.Errorf("Parse(production, invalid) = %v, %v; want nil, nil", inj, err)
	}
}

func TestEmptySpecIsInert(t *testing.T) {
	inj, err := Parse(TestEnv, "  ")
	if inj != nil || err != nil {
		t.Errorf("Parse(test, blank) = %v, %v", inj, err)
	}
}

func TestPointFailsTheConfiguredNumberOfTimes(t *testing.T) {
	inj, err := Parse(TestEnv, "posting.before_commit:2")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := inj.Hit(PostingBeforeCommit, "receipt.post"); !errors.Is(err, ErrInjected) {
			t.Fatalf("hit %d = %v, want ErrInjected", i+1, err)
		}
	}
	if err := inj.Hit(PostingBeforeCommit, "receipt.post"); err != nil {
		t.Errorf("third hit = %v, want nil once the count is used up", err)
	}
	if inj.Armed() {
		t.Error("Armed after every hit was used")
	}
}

func TestScopedPointFailsOnlyThatScope(t *testing.T) {
	inj, err := Parse(TestEnv, "posting.before_commit@pos.checkout")
	if err != nil {
		t.Fatal(err)
	}
	if err := inj.Hit(PostingBeforeCommit, "receipt.post"); err != nil {
		t.Errorf("other scope = %v, want nil", err)
	}
	if !inj.Armed() {
		t.Error("a hit of another scope consumed the fault")
	}
	if err := inj.Hit(PostingBeforeCommit, "pos.checkout"); !errors.Is(err, ErrInjected) {
		t.Errorf("armed scope = %v, want ErrInjected", err)
	}
	if err := inj.Hit(PostingBeforeCommit, "pos.checkout"); err != nil {
		t.Errorf("second hit = %v, want nil (default count is 1)", err)
	}
}

func TestParseRejectsMalformedSpecs(t *testing.T) {
	for _, spec := range []string{"posting.after_commit", "posting.before_commit:0", "posting.before_commit:x",
		"posting.before_commit,", "@pos.checkout"} {
		if _, err := Parse(TestEnv, spec); err == nil || !strings.Contains(err.Error(), EnvVar) {
			t.Errorf("Parse(%q) = %v, want an error naming %s", spec, err, EnvVar)
		}
	}
}

func TestConcurrentHitsConsumeEachFailureOnce(t *testing.T) {
	inj, err := Parse(TestEnv, "posting.before_commit:10")
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed int
	)
	for range 50 {
		wg.Go(func() {
			if inj.Hit(PostingBeforeCommit, "") != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if failed != 10 {
		t.Errorf("failed hits = %d, want 10", failed)
	}
}
