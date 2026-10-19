package mailer

import (
	"bufio"
	"context"
	"net"
	"net/mail"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func addr(s string) *mail.Address { return &mail.Address{Address: s} }

func TestMessageValidate(t *testing.T) {
	ok := Message{To: []*mail.Address{addr("a@example.com")}, Subject: "Hi", Text: "body"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Message{
		{Subject: "Hi", Text: "b"},
		{To: []*mail.Address{nil}, Subject: "Hi", Text: "b"},
		{To: []*mail.Address{{Name: "x\r\nBcc: evil@example.com", Address: "a@example.com"}}, Subject: "Hi", Text: "b"},
		{To: []*mail.Address{addr("a@example.com")}, Subject: "Hi\r\nBcc: x", Text: "b"},
		{To: []*mail.Address{addr("a@example.com")}, Subject: "Hi"},
	}
	for i, m := range bad {
		if m.Validate() == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func testFS() fstest.MapFS {
	layout, err := Templates.ReadFile("templates/layout.html.tmpl")
	if err != nil {
		panic(err)
	}
	return fstest.MapFS{
		"templates/layout.html.tmpl":     {Data: layout},
		"templates/welcome.subject.tmpl": {Data: []byte("Chào {{.Name}}\n  tới Tally")},
		"templates/welcome.text.tmpl":    {Data: []byte("Xin chào {{.Name}}: {{.Link}}")},
		"templates/welcome.html.tmpl":    {Data: []byte(`{{define "content"}}<a href="{{.Link}}">{{.Name}}</a>{{end}}`)},
		"templates/plain.subject.tmpl":   {Data: []byte("Plain")},
		"templates/plain.text.tmpl":      {Data: []byte("only text")},
	}
}

func TestRender(t *testing.T) {
	r := NewRenderer(testFS())
	out, err := r.Render("welcome", map[string]string{"Name": "<An>", "Link": "https://demo.tally.vn/x"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject != "Chào <An> tới Tally" {
		t.Errorf("subject %q", out.Subject)
	}
	if out.Text != "Xin chào <An>: https://demo.tally.vn/x" {
		t.Errorf("text %q", out.Text)
	}
	if !strings.Contains(out.HTML, "&lt;An&gt;") || !strings.Contains(out.HTML, `href="https://demo.tally.vn/x"`) || !strings.Contains(out.HTML, "<!doctype html>") {
		t.Errorf("html %q", out.HTML)
	}

	plain, err := r.Render("plain", nil)
	if err != nil || plain.HTML != "" || plain.Text != "only text" {
		t.Fatalf("plain = %+v, %v", plain, err)
	}
	for _, name := range []string{"", "../etc", "a.b", "missing"} {
		if _, err := r.Render(name, nil); err == nil {
			t.Errorf("Render(%q) succeeded", name)
		}
	}
	if _, err := r.Render("welcome", map[string]string{"Name": "x"}); err == nil {
		t.Error("missing key accepted")
	}
}

func TestBuiltInLayoutParses(t *testing.T) {
	if _, err := Templates.ReadFile("templates/layout.html.tmpl"); err != nil {
		t.Fatal(err)
	}
}

func newTestSMTP(t *testing.T, cfg SMTPConfig) *SMTP {
	t.Helper()
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC) }
	return s
}

func TestBuild(t *testing.T) {
	s := newTestSMTP(t, SMTPConfig{Host: "localhost", Port: 1025, From: &mail.Address{Name: "Tally", Address: "no-reply@tally.vn"}})
	plain, err := s.build(Message{To: []*mail.Address{addr("a@example.com")}, Subject: "Đơn hàng", Text: "Tổng: 1.000 ₫"})
	if err != nil {
		t.Fatal(err)
	}
	p := string(plain)
	for _, want := range []string{
		`From: "Tally" <no-reply@tally.vn>`, "To: <a@example.com>", "Subject: =?utf-8?q?",
		"Date: Sun, 01 Mar 2026 08:00:00 +0000", "@tally.vn>\r\n", `Content-Type: text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding: quoted-printable",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plain message lacks %q:\n%s", want, p)
		}
	}
	multi, err := s.build(Message{To: []*mail.Address{addr("a@example.com")}, Subject: "S", Text: "t", HTML: "<p>h</p>"})
	if err != nil {
		t.Fatal(err)
	}
	m := string(multi)
	if !strings.Contains(m, "multipart/alternative") || !strings.Contains(m, "Content-Type: text/html") || !strings.Contains(m, "Content-Type: text/plain") {
		t.Errorf("multipart message:\n%s", m)
	}
}

func TestNewSMTPValidation(t *testing.T) {
	from := &mail.Address{Address: "a@b.c"}
	for i, cfg := range []SMTPConfig{{Port: 25, From: from}, {Host: "h", From: from}, {Host: "h", Port: 25}} {
		if _, err := NewSMTP(cfg); err == nil {
			t.Errorf("config %d accepted", i)
		}
	}
}

// A server without STARTTLS must never receive credentials.
func TestSendRefusesCredentialsWithoutStartTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		_, _ = rw.WriteString("220 test ESMTP\r\n")
		_ = rw.Flush()
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(strings.ToUpper(line), "AUTH") {
				t.Error("credentials sent over plain text")
			}
			_, _ = rw.WriteString("250 ok\r\n")
			_ = rw.Flush()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	var portNum int
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}
	s := newTestSMTP(t, SMTPConfig{Host: host, Port: portNum, User: "u", Password: "p", From: addr("a@b.c"), Timeout: 5 * time.Second})
	err = s.Send(context.Background(), Message{To: []*mail.Address{addr("x@y.z")}, Subject: "S", Text: "t"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("err = %v", err)
	}
	if err := s.Send(context.Background(), Message{Subject: "S", Text: "t"}); err == nil {
		t.Fatal("invalid message sent")
	}
}
