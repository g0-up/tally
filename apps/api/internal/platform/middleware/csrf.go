package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// CSRFHeader carries the double-submit token.
const CSRFHeader = "X-CSRF-Token"

const csrfNonceBytes = 16

// CSRFTokens issues and checks CSRF tokens bound to the request's session. A token is
// "nonce.mac" where mac = HMAC-SHA256(key, binding || nonce): a token minted for one session (or
// for the anonymous state of one host) is worthless for any other, so a cookie planted by a
// sibling subdomain or left over from a previous session never passes.
type CSRFTokens struct {
	key []byte
}

// NewCSRFTokens returns an issuer keyed by key. An empty key gets a random per-process key,
// which only suits a single development process.
func NewCSRFTokens(key []byte) *CSRFTokens {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("csrf: crypto/rand failed: " + err.Error())
		}
	}
	return &CSRFTokens{key: append([]byte(nil), key...)}
}

// Issue returns a fresh token for binding.
func (t *CSRFTokens) Issue(binding string) string {
	nonce := make([]byte, csrfNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		panic("csrf: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(nonce) + "." + base64.RawURLEncoding.EncodeToString(t.mac(binding, nonce))
}

// Valid reports whether token was issued by Issue for binding.
func (t *CSRFTokens) Valid(binding, token string) bool {
	rawNonce, rawMAC, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(rawNonce)
	if err != nil || len(nonce) != csrfNonceBytes {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(rawMAC)
	if err != nil {
		return false
	}
	return hmac.Equal(got, t.mac(binding, nonce))
}

func (t *CSRFTokens) mac(binding string, nonce []byte) []byte {
	h := hmac.New(sha256.New, t.key)
	h.Write([]byte(binding))
	h.Write([]byte{0})
	h.Write(nonce)
	return h.Sum(nil)
}

// CSRFBinding is what the CSRF token of the request is bound to: the resolved session, or for an
// anonymous request (sign-up, login, password reset) the host it is served on.
func CSRFBinding(c *gin.Context) string {
	if p, ok := PrincipalOf(c); ok {
		return SessionBinding(p.Kind, p.SessionID)
	}
	host, _ := httpx.HostOf(c)
	return AnonymousBinding(host)
}

// SessionBinding is the CSRF binding of a session (used when a login issues its token).
func SessionBinding(kind PrincipalKind, sessionID uuid.UUID) string {
	return "session:" + string(kind) + ":" + sessionID.String()
}

// AnonymousBinding is the CSRF binding of a signed-out request on host.
func AnonymousBinding(host httpx.Host) string {
	return "anon:" + string(host.Kind) + ":" + host.Slug
}

// CSRF enforces the double-submit check on state-changing methods: the X-CSRF-Token header must
// equal the CSRF cookie (constant-time compare) and be a token issued for the request's session
// binding (see CSRFBinding). Safe methods pass. It must run after Session.
func CSRF(tokens *CSRFTokens, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if httpx.MethodIsSafe(c.Request.Method) {
			c.Next()
			return
		}
		cookie, err := c.Cookie(cookieName)
		header := c.GetHeader(CSRFHeader)
		if err != nil || cookie == "" || header == "" || subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 ||
			!tokens.Valid(CSRFBinding(c), header) {
			httpx.RenderError(c, apperr.NewForbidden("csrf_failed", "The request could not be verified; reload the page and try again"))
			return
		}
		c.Next()
	}
}

// SetCSRFCookie issues a token for binding, stores it in the CSRF cookie and returns it. The
// cookie is host-only and readable by the page script, which echoes it in X-CSRF-Token.
func SetCSRFCookie(c *gin.Context, tokens *CSRFTokens, cookies Cookies, name, binding string) string {
	token := tokens.Issue(binding)
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Value: token, Path: "/",
		Secure: cookies.Secure, HttpOnly: false, SameSite: http.SameSiteLaxMode,
	})
	return token
}

// SetSessionCookie stores an opaque session token in a host-only HttpOnly cookie. maxAge 0 makes
// it a browser-session cookie; the server-side expiry is authoritative either way.
func SetSessionCookie(c *gin.Context, cookies Cookies, name, token string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Value: token, Path: "/", MaxAge: maxAge,
		Secure: cookies.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie deletes a host-only cookie.
func ClearCookie(c *gin.Context, cookies Cookies, name string, httpOnly bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		Secure: cookies.Secure, HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode,
	})
}
