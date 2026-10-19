package httpx

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// CommandRoute is one documented custom-method route, e.g. POST /api/v1/invoices/{id}:confirm.
type CommandRoute struct {
	Method string
	// Path is the OpenAPI-style path ("{id}" placeholders).
	Path string
	// GinPath is the gin route that serves it ("/api/v1/invoices/:id" for item verbs).
	GinPath string
}

// CommandIndex collects the custom-method routes registered through CommandRouters, so the
// OpenAPI contract test can expand the single gin dispatch route into documented paths.
type CommandIndex struct {
	mu     sync.Mutex
	routes []CommandRoute
}

func (x *CommandIndex) add(r CommandRoute) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.routes = append(x.routes, r)
}

// Routes returns the recorded routes sorted by path.
func (x *CommandIndex) Routes() []CommandRoute {
	if x == nil {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	out := append([]CommandRoute(nil), x.routes...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// CommandRouter serves custom methods of one resource: item verbs `POST /<resource>/{id}:<verb>`
// and collection verbs `POST /<resource>:<verb>`. Gin reads ':' as a parameter marker, so item
// verbs share one `POST /<resource>/:id` route whose value is split on the last ':' (the id must
// be a UUID); collection verbs are registered as literal paths with an escaped colon. An unknown
// verb or a malformed id answers 404.
type CommandRouter struct {
	group    *gin.RouterGroup
	resource string
	index    *CommandIndex
	verbs    map[string]gin.HandlerFunc
	// key names the item placeholder of a keyed router ("stepKey"); empty means a UUID id.
	key string
}

// NewCommandRouter returns the router for resource (plural kebab-case, e.g. "invoices") under
// group. index may be nil.
func NewCommandRouter(group *gin.RouterGroup, resource string, index *CommandIndex) *CommandRouter {
	return &CommandRouter{group: group, resource: strings.Trim(resource, "/"), index: index}
}

// NewKeyedCommandRouter returns a router whose items are named by a key instead of a UUID:
// `POST /<resource>/{key}:<verb>`, the key made of [a-z0-9_] (e.g. onboarding step keys). The
// handler reads it with CommandKey; keyName is the documented placeholder.
func NewKeyedCommandRouter(group *gin.RouterGroup, resource, keyName string, index *CommandIndex) *CommandRouter {
	if keyName == "" {
		panic("command router: empty key name")
	}
	r := NewCommandRouter(group, resource, index)
	r.key = keyName
	return r
}

func validKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func validVerb(verb string) bool {
	if verb == "" {
		return false
	}
	for _, r := range verb {
		if (r < 'a' || r > 'z') && r != '-' {
			return false
		}
	}
	return true
}

// Item registers handler for POST /<resource>/{id}:<verb>. The handler reads the id with CommandID.
func (r *CommandRouter) Item(verb string, handler gin.HandlerFunc) {
	if !validVerb(verb) {
		panic(fmt.Sprintf("command router %s: invalid verb %q", r.resource, verb))
	}
	if r.verbs == nil {
		r.verbs = map[string]gin.HandlerFunc{}
		r.group.POST("/"+r.resource+"/:id", r.dispatch)
	}
	if _, dup := r.verbs[verb]; dup {
		panic(fmt.Sprintf("command router %s: verb %q registered twice", r.resource, verb))
	}
	r.verbs[verb] = handler
	base := joinPath(r.group.BasePath(), r.resource)
	placeholder := "{id}"
	if r.key != "" {
		placeholder = "{" + r.key + "}"
	}
	r.index.add(CommandRoute{Method: http.MethodPost, Path: base + "/" + placeholder + ":" + verb, GinPath: base + "/:id"})
}

// Collection registers handler for POST /<resource>:<verb>.
func (r *CommandRouter) Collection(verb string, handler gin.HandlerFunc) {
	if !validVerb(verb) {
		panic(fmt.Sprintf("command router %s: invalid verb %q", r.resource, verb))
	}
	r.group.POST("/"+r.resource+`\:`+verb, handler)
	path := joinPath(r.group.BasePath(), r.resource) + ":" + verb
	r.index.add(CommandRoute{Method: http.MethodPost, Path: path, GinPath: path})
}

const (
	commandIDKey  = "httpx.command_id"
	commandKeyKey = "httpx.command_key"
)

func (r *CommandRouter) dispatch(c *gin.Context) {
	target := c.Param("id")
	i := strings.LastIndexByte(target, ':')
	if i < 0 {
		RenderError(c, apperr.NewNotFound("not_found", "Not found"))
		return
	}
	h, ok := r.verbs[target[i+1:]]
	if !ok {
		RenderError(c, apperr.NewNotFound("not_found", "Not found"))
		return
	}
	if r.key != "" {
		if !validKey(target[:i]) {
			RenderError(c, apperr.NewNotFound("not_found", "Not found"))
			return
		}
		c.Set(commandKeyKey, target[:i])
		h(c)
		return
	}
	id, err := ids.Parse(target[:i])
	if err != nil {
		RenderError(c, apperr.NewNotFound("not_found", "Not found"))
		return
	}
	c.Set(commandIDKey, id)
	h(c)
}

// CommandID returns the resource id of an item verb request.
func CommandID(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(commandIDKey); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}

// CommandKey returns the item key of a keyed router request.
func CommandKey(c *gin.Context) string { return c.GetString(commandKeyKey) }

// PathID parses a ":name" path parameter as a UUID; a malformed id is 404 (the resource cannot
// exist), never 422.
func PathID(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := ids.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, apperr.NewNotFound("not_found", "Not found")
	}
	return id, nil
}

func joinPath(base, resource string) string {
	return strings.TrimRight(base, "/") + "/" + resource
}
