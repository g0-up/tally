//go:build integration

package mailer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type mailpitMessage struct {
	Subject string
	Text    string
	HTML    string
	From    struct{ Address string }
	To      []struct{ Address string }
}

// fetchFromMailpit finds the message whose subject contains token through the Mailpit API of the
// dev stack (MAILPIT_URL, default http://localhost:8025).
func fetchFromMailpit(t *testing.T, token string) mailpitMessage {
	t.Helper()
	base := env("MAILPIT_URL", "http://localhost:8025")
	client := &http.Client{Timeout: 5 * time.Second}
	getJSON := func(u string, v any) error {
		resp, err := client.Get(u) //nolint:noctx // test helper
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: %d", u, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list struct{ Messages []struct{ ID string } }
		err := getJSON(base+"/api/v1/search?query="+url.QueryEscape("subject:"+token), &list)
		if err == nil && len(list.Messages) > 0 {
			var msg mailpitMessage
			if err := getJSON(base+"/api/v1/message/"+list.Messages[0].ID, &msg); err != nil {
				t.Fatal(err)
			}
			return msg
		}
		if time.Now().After(deadline) {
			t.Fatalf("message %s not found in Mailpit (start it with `make db-up`): %v", token, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestSendThroughMailpit(t *testing.T) {
	port, err := strconv.Atoi(env("SMTP_PORT", "1025"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := mailer.NewSMTP(mailer.SMTPConfig{
		Host: env("SMTP_HOST", "localhost"), Port: port,
		From: &mail.Address{Name: "Tally", Address: "no-reply@tally.localhost"}, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.ReplaceAll(ids.New().String(), "-", "")
	msg := mailer.Message{
		To:      []*mail.Address{{Name: "Chủ cửa hàng", Address: "owner@example.com"}},
		Subject: "Xác nhận email " + token,
		Text:    "Mã của bạn: 123 456 – cảm ơn!",
		HTML:    "<p>Mã của bạn: <b>123 456</b></p>",
	}
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("send through Mailpit (start it with `make db-up`): %v", err)
	}
	got := fetchFromMailpit(t, token)
	if got.Subject != msg.Subject || !strings.Contains(got.Text, "123 456 – cảm ơn!") || !strings.Contains(got.HTML, "<b>123 456</b>") {
		t.Errorf("received %+v", got)
	}
	if got.From.Address != "no-reply@tally.localhost" || len(got.To) != 1 || got.To[0].Address != "owner@example.com" {
		t.Errorf("envelope %+v", got)
	}
}
