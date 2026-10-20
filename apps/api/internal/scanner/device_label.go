package scanner

import "strings"

// maxUserAgentLen bounds how much of a User-Agent is inspected.
const maxUserAgentLen = 512

// uaMatch maps a User-Agent token to a display name; the first match wins, so more specific
// tokens come first.
type uaMatch struct{ token, name string }

var (
	uaSystems = []uaMatch{
		{"iphone", "iPhone"}, {"ipad", "iPad"}, {"android", "Android"}, {"windows", "Windows"},
		{"cros ", "ChromeOS"}, {"macintosh", "Mac"}, {"mac os x", "Mac"}, {"linux", "Linux"},
	}
	// Browsers built on Chrome or Safari carry their engine's token too, so they come first.
	uaBrowsers = []uaMatch{
		{"edg/", "Edge"}, {"edga/", "Edge"}, {"edgios/", "Edge"}, {"opr/", "Opera"}, {"samsungbrowser", "Samsung Internet"}, {"zalo", "Zalo"},
		{"fban", "Facebook"}, {"fbav", "Facebook"}, {"firefox", "Firefox"}, {"fxios", "Firefox"},
		{"crios", "Chrome"}, {"chrome", "Chrome"}, {"safari", "Safari"},
	}
)

// DeviceLabel reduces a User-Agent to "system · browser", such as "iPhone · Safari", for the
// desktop to show next to the verify code. The User-Agent itself is never stored. It returns ""
// when neither part is recognised.
func DeviceLabel(userAgent string) string {
	if len(userAgent) > maxUserAgentLen {
		userAgent = userAgent[:maxUserAgentLen]
	}
	ua := strings.ToLower(userAgent)
	system, browser := firstMatch(ua, uaSystems), firstMatch(ua, uaBrowsers)
	switch {
	case system != "" && browser != "":
		return system + " · " + browser
	case system != "":
		return system
	default:
		return browser
	}
}

func firstMatch(ua string, matches []uaMatch) string {
	for _, m := range matches {
		if strings.Contains(ua, m.token) {
			return m.name
		}
	}
	return ""
}
