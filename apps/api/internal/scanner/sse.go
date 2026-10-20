package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Stream timing. The server's WriteTimeout (60 s) would cut a stream, so every write pushes the
// write deadline writeWindow ahead instead.
const (
	writeWindow = 30 * time.Second
	// pingEvery keeps proxies from closing an idle stream.
	pingEvery = 15 * time.Second
	// tickEvery is how often a stream re-checks its session in the background.
	tickEvery = 10 * time.Second
	// tickTimeout bounds one background check.
	tickTimeout = 2 * time.Second
	// maxTickFailures consecutive failed checks close the stream so the client reconnects.
	maxTickFailures = 3
	// retryMillis is the reconnect delay EventSource uses after a dropped stream.
	retryMillis = 3000
)

// sseWriter writes server-sent events to one response.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// startSSE sends the stream headers. The read deadline is lifted: when the server's ReadTimeout
// expires, net/http cancels the request context of a handler that is still running.
func startSSE(w http.ResponseWriter) (*sseWriter, error) {
	s := &sseWriter{w: w, rc: http.NewResponseController(w)}
	if err := s.rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, err
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store, no-transform")
	h.Set("X-Accel-Buffering", "no")
	if err := s.extend(); err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", retryMillis); err != nil {
		return nil, err
	}
	return s, s.rc.Flush()
}

// event writes one named event with data encoded as JSON.
func (s *sseWriter) event(ev Event) error {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return fmt.Errorf("scanner: encode %s event: %w", ev.Name, err)
	}
	return s.write("event: " + ev.Name + "\ndata: " + string(data) + "\n\n")
}

// ping writes a comment line that keeps the connection busy.
func (s *sseWriter) ping() error { return s.write(": ping\n\n") }

func (s *sseWriter) write(chunk string) error {
	if err := s.extend(); err != nil {
		return err
	}
	if _, err := s.w.Write([]byte(chunk)); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *sseWriter) extend() error {
	err := s.rc.SetWriteDeadline(time.Now().Add(writeWindow))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// tickResult is what one background check decided: events to write, then whether the stream
// ends. A failed check carries err.
type tickResult struct {
	events []Event
	end    bool
	err    error
}

// streamTiming tunes a stream; tests shorten it.
type streamTiming struct {
	ping, tick time.Duration
}

// pump writes first, then the events of the hub channel, a ping every interval and the events of
// the background checks, until the client leaves, the channel closes, a terminal event is
// written or a check ends the stream. The loop never waits for the database: check runs in its
// own goroutine with a timeout, and a tick is skipped while the previous check runs.
func pump(ctx context.Context, w *sseWriter, first Event, events <-chan Event, terminal func(name string) bool,
	timing streamTiming, check func(ctx context.Context) tickResult,
) {
	if w.event(first) != nil {
		return
	}
	pinger, ticker := time.NewTicker(timing.ping), time.NewTicker(timing.tick)
	defer pinger.Stop()
	defer ticker.Stop()
	results := make(chan tickResult, 1)
	running, failures := false, 0
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok || w.event(ev) != nil || terminal(ev.Name) {
				return
			}
		case <-pinger.C:
			if w.ping() != nil {
				return
			}
		case <-ticker.C:
			if running {
				continue
			}
			running = true
			go func() {
				tctx, cancel := context.WithTimeout(ctx, tickTimeout)
				defer cancel()
				results <- check(tctx)
			}()
		case r := <-results:
			running = false
			if r.err != nil {
				if failures++; failures >= maxTickFailures {
					return
				}
				continue
			}
			failures = 0
			for _, ev := range r.events {
				if w.event(ev) != nil {
					return
				}
			}
			if r.end {
				return
			}
		}
	}
}
