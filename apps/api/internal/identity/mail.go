package identity

import (
	"context"
	"net/mail"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
)

// Mail templates (internal/platform/mailer/templates).
const (
	mailVerifyEmail   = "verify_email"
	mailResetPassword = "reset_password"
	mailOwnerTransfer = "owner_transfer"
	mailNewDevice     = "new_device"
	mailStoreLookup   = "store_lookup"
	mailAccountExists = "account_exists"
	mailClosureVerify = "closure_verify"
	mailClosureExport = "closure_export"
)

// mailTimeout bounds one delivery attempt.
const mailTimeout = 30 * time.Second

// mailData is the data of every identity template. Links are absolute and built by urls.Builder.
type mailData struct {
	Name      string
	StoreName string
	StoreURL  string
	Link      string
	Device    string
	Time      string
}

// send renders a template and delivers it in the background, so the response time never depends
// on whether a mail was sent (sign-up and store lookup must answer alike either way). Failures
// are logged without the address. WaitMail waits for deliveries in flight.
func (s *Service) send(template, to string, data mailData) {
	if s.mailer == nil || s.renderer == nil {
		s.logger.Warn("mail not sent: no mailer configured", "template", template)
		return
	}
	rendered, err := s.renderer.Render(template, data)
	if err != nil {
		s.logger.Error("mail not sent: render failed", "template", template, "error", err.Error())
		return
	}
	msg := mailer.Message{
		To:      []*mail.Address{{Name: data.Name, Address: to}},
		Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML,
	}
	s.mailWG.Add(1)
	go func() {
		defer s.mailWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
		defer cancel()
		if err := s.mailer.Send(ctx, msg); err != nil {
			s.logger.Error("mail not sent", "template", template, "error", err.Error())
		}
	}()
}

// WaitMail blocks until every mail queued so far was handed to the relay (or failed). Commands
// call it before exiting; the server calls it during shutdown.
func (s *Service) WaitMail() { s.mailWG.Wait() }

// localTime formats t for a mail in the tenant time zone.
func (s *Service) localTime(t time.Time) string {
	return t.In(s.location).Format("15:04 02/01/2006")
}
