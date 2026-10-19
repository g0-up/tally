// Package fault lets an acceptance test make the server fail at a named point, to prove that a
// crash in the middle of a posting leaves nothing behind and that a retry with the same
// Idempotency-Key posts exactly once.
//
// The hook is compiled into every binary but is honoured only when APP_ENV=test: Parse returns a
// nil Injector in any other environment, and every method of a nil Injector is a no-op. Product
// behaviour never depends on it.
package fault

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// EnvVar is the environment variable that arms the hook (only with APP_ENV=test).
const EnvVar = "TALLY_FAULT_INJECT"

// TestEnv is the only APP_ENV value under which the hook is honoured.
const TestEnv = "test"

// Point names a place where a failure can be injected.
type Point string

// PostingBeforeCommit fails a posting after every row, audit event, outbox event and the
// idempotency record have been written, just before the transaction commits: the worst moment for
// a crash, since all of the posting must roll back.
const PostingBeforeCommit Point = "posting.before_commit"

var knownPoints = map[Point]bool{PostingBeforeCommit: true}

// ErrInjected is the error an armed point returns.
var ErrInjected = errors.New("fault: injected failure")

// rule arms one point, for one scope or any ("") and a number of hits.
type rule struct {
	point     Point
	scope     string
	remaining int
}

// Injector holds the armed points. A nil Injector never fails.
type Injector struct {
	mu    sync.Mutex
	rules []*rule
}

// Parse reads the TALLY_FAULT_INJECT value for the environment appEnv. Outside APP_ENV=test, or
// with an empty spec, it returns nil: the hook is inert.
//
// The spec is a comma-separated list of "point[@scope][:count]": the point fails count times
// (default 1), only for postings of that scope when one is given, then behaves normally. For
// example "posting.before_commit@pos.checkout" fails the next POS checkout once.
func Parse(appEnv, spec string) (*Injector, error) {
	spec = strings.TrimSpace(spec)
	if appEnv != TestEnv || spec == "" {
		return nil, nil //nolint:nilnil // a nil Injector is the inert hook, not a missing value
	}
	inj := &Injector{}
	for entry := range strings.SplitSeq(spec, ",") {
		r, err := parseRule(strings.TrimSpace(entry))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvVar, err)
		}
		inj.rules = append(inj.rules, r)
	}
	return inj, nil
}

func parseRule(entry string) (*rule, error) {
	r := &rule{remaining: 1}
	if head, count, ok := strings.Cut(entry, ":"); ok {
		n, err := strconv.Atoi(count)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("count of %q must be a positive integer", entry)
		}
		entry, r.remaining = head, n
	}
	point, scope, _ := strings.Cut(entry, "@")
	r.point, r.scope = Point(point), scope
	if !knownPoints[r.point] {
		return nil, fmt.Errorf("unknown fault point %q", point)
	}
	return r, nil
}

// Hit returns ErrInjected when p is armed for scope and has hits left, consuming one; otherwise
// nil. It is safe for concurrent use and always nil on a nil Injector.
func (i *Injector) Hit(p Point, scope string) error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, r := range i.rules {
		if r.point == p && (r.scope == "" || r.scope == scope) && r.remaining > 0 {
			r.remaining--
			return fmt.Errorf("%w at %s (%s)", ErrInjected, p, scope)
		}
	}
	return nil
}

// Armed reports whether any point still has hits left (false on a nil Injector).
func (i *Injector) Armed() bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, r := range i.rules {
		if r.remaining > 0 {
			return true
		}
	}
	return false
}
