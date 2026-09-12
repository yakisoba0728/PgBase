package pgbase

import (
	"io/fs"
	"net/http"
	"testing"
	"testing/fstest"
)

func TestRouteGroupComposesPrefixMiddlewareStreamAndStatic(t *testing.T) {
	order := []string{}
	middleware := func(name string) RouteMiddleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" before")
				next.ServeHTTP(w, r)
				order = append(order, name+" after")
			})
		}
	}
	group := NewRouteGroup("/application", middleware("outer"))
	child := group.Group("reports", middleware("inner"))
	child.Stream(http.MethodGet, "/events", RoutePublic, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }))
	child.Static("assets", RoutePublic, fstest.MapFS{"index.txt": &fstest.MapFile{Data: []byte("ok"), Mode: fs.FileMode(0o444)}})
	routes := group.Routes()
	if len(routes) != 2 || routes[0].Pattern != "/application/reports/events" || !routes[0].StreamResponse || routes[1].Pattern != "/application/reports/assets/{path...}" {
		t.Fatalf("routes=%+v", routes)
	}
	routes[0].Handler.ServeHTTP(nilResponseWriter{}, nil)
	want := []string{"outer before", "inner before", "handler", "inner after", "outer after"}
	if len(order) != len(want) {
		t.Fatalf("order=%v", order)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("order=%v", order)
		}
	}
}

type nilResponseWriter struct{}

func (nilResponseWriter) Header() http.Header       { return http.Header{} }
func (nilResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (nilResponseWriter) WriteHeader(int)           {}
