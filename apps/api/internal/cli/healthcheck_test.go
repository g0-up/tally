package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckHealth(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		delay   time.Duration
		wantErr bool
	}{
		{name: "ok", status: http.StatusOK},
		{name: "server error", status: http.StatusServiceUnavailable, wantErr: true},
		{name: "timeout", status: http.StatusOK, delay: 200 * time.Millisecond, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.delay > 0 {
					select {
					case <-time.After(tc.delay):
					case <-r.Context().Done():
						return
					}
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			err := checkHealth(context.Background(), srv.Client(), srv.URL+"/healthz", 50*time.Millisecond)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkHealth error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestCheckHealthUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	if err := checkHealth(context.Background(), http.DefaultClient, url, time.Second); err == nil {
		t.Fatal("checkHealth succeeded against a closed server, want error")
	}
}
