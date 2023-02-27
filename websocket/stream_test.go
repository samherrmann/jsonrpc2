package websocket_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
)

func TestObjectStream(t *testing.T) {

	ctx := context.Background()
	done := make(chan struct{})

	ha := testHandlerA{t: t}
	upgrader := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		jc := jsonrpc2.NewConn(r.Context(), websocketjsonrpc2.NewObjectStream(c), &ha)
		<-jc.DisconnectNotify()
		close(done)
	}))
	defer s.Close()

	c, resp, err := websocket.DefaultDialer.Dial(strings.Replace(s.URL, "http:", "ws:", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	defer c.Close()
	testClientServer(ctx, t, websocketjsonrpc2.NewObjectStream(c))

	<-done // keep the test running until the WebSocket disconnects (to avoid missing errors)
}

func testClientServer(ctx context.Context, t *testing.T, stream jsonrpc2.ObjectStream) {
	hb := testHandlerB{t: t}
	cc := jsonrpc2.NewConn(ctx, stream, &hb)
	defer func() {
		if err := cc.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Simple
	const n = 100
	for i := 0; i < n; i++ {
		var got string
		if err := cc.Call(ctx, "f", []int32{1, 2, 3}, &got); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("hello, #%d: [1,2,3]", i); got != want {
			t.Errorf("got result %q, want %q", got, want)
		}
	}
	time.Sleep(100 * time.Millisecond)
	hb.mu.Lock()
	got := hb.got
	hb.mu.Unlock()
	if len(got) != n {
		t.Errorf("testHandlerB got %d notifications, want %d", len(hb.got), n)
	}
	// Ensure messages are in order since we are not using the async handler.
	for i, s := range got {
		want := fmt.Sprintf(`"notif for #%d"`, i)
		if s != want {
			t.Fatalf("out of order response. got %q, want %q", s, want)
		}
	}
}

// testHandlerA is the "server" handler.
type testHandlerA struct{ t *testing.T }

func (h *testHandlerA) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req.Notif {
		return // notification
	}
	if err := conn.Reply(ctx, req.ID, fmt.Sprintf("hello, #%s: %s", req.ID, *req.Params)); err != nil {
		h.t.Error(err)
	}

	if err := conn.Notify(ctx, "m", fmt.Sprintf("notif for #%s", req.ID)); err != nil {
		h.t.Error(err)
	}
}

// testHandlerB is the "client" handler.
type testHandlerB struct {
	t   *testing.T
	mu  sync.Mutex
	got []string
}

func (h *testHandlerB) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req.Notif {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.got = append(h.got, string(*req.Params))
		return
	}
	h.t.Errorf("testHandlerB got unexpected request %+v", req)
}
