// Package mailer sends transactional mail (verification, password reset, export ready …).
// Templates live in templates/ as <name>.subject.tmpl, <name>.text.tmpl and <name>.html.tmpl;
// HTML bodies render inside layout.html.tmpl with html/template escaping. Absolute links in mail
// come from urls.Builder only.
package mailer

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"net/mail"
	"strings"
	texttemplate "text/template"
)

// Templates are the built-in mail templates.
//
//go:embed templates/*.tmpl
var Templates embed.FS

// Message is one mail.
type Message struct {
	To      []*mail.Address
	Subject string
	Text    string // plain text part, required
	HTML    string // optional HTML alternative
}

// Validate rejects messages that cannot be sent safely.
func (m Message) Validate() error {
	if len(m.To) == 0 {
		return errors.New("mail: no recipient")
	}
	for _, a := range m.To {
		if a == nil || a.Address == "" || strings.ContainsAny(a.Address+a.Name, "\r\n") {
			return errors.New("mail: invalid recipient")
		}
	}
	if m.Subject == "" || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: subject is empty or contains a line break")
	}
	if m.Text == "" {
		return errors.New("mail: text body is required")
	}
	return nil
}

// Mailer is the port domains use to send mail.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Renderer turns a template name and data into subject and bodies.
type Renderer struct {
	fsys fs.FS
}

// NewRenderer reads templates from fsys (Templates in production).
func NewRenderer(fsys fs.FS) *Renderer { return &Renderer{fsys: fsys} }

// Rendered is the output of Render.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// Render executes <name>.subject.tmpl and <name>.text.tmpl (required) and <name>.html.tmpl
// (optional, inside layout.html.tmpl) with data.
func (r *Renderer) Render(name string, data any) (Rendered, error) {
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return Rendered{}, fmt.Errorf("mail template: invalid name %q", name)
	}
	subject, err := r.text(name+".subject.tmpl", data)
	if err != nil {
		return Rendered{}, err
	}
	subject = strings.Join(strings.Fields(subject), " ") // one line, no header injection
	text, err := r.text(name+".text.tmpl", data)
	if err != nil {
		return Rendered{}, err
	}
	html, err := r.html(name+".html.tmpl", data)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{Subject: subject, Text: text, HTML: html}, nil
}

func (r *Renderer) text(file string, data any) (string, error) {
	t, err := texttemplate.New(file).Option("missingkey=error").ParseFS(r.fsys, "templates/"+file)
	if err != nil {
		return "", fmt.Errorf("mail template %s: %w", file, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("mail template %s: %w", file, err)
	}
	return buf.String(), nil
}

func (r *Renderer) html(file string, data any) (string, error) {
	if _, err := fs.Stat(r.fsys, "templates/"+file); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	t, err := htmltemplate.New("layout.html.tmpl").Option("missingkey=error").
		ParseFS(r.fsys, "templates/layout.html.tmpl", "templates/"+file)
	if err != nil {
		return "", fmt.Errorf("mail template %s: %w", file, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("mail template %s: %w", file, err)
	}
	return buf.String(), nil
}
