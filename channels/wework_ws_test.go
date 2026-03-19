package channels

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/smallnest/goclaw/bus"
	"github.com/smallnest/goclaw/config"
)

func TestWeWorkLongConnStatePopReplyContext(t *testing.T) {
	state := newWeWorkLongConnState()
	state.storeReplyContext(&weworkReplyContext{
		MessageID: "msg-1",
		ReqID:     "req-1",
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    "chat-1",
		ChatType:  "single",
	})
	state.storeReplyContext(&weworkReplyContext{
		MessageID: "msg-2",
		ReqID:     "req-2",
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    "chat-1",
		ChatType:  "single",
	})

	byReply := state.popReplyContext("msg-2", "chat-1")
	if byReply == nil || byReply.ReqID != "req-2" {
		t.Fatalf("expected msg-2 context, got %#v", byReply)
	}

	byChat := state.popReplyContext("", "chat-1")
	if byChat == nil || byChat.ReqID != "req-1" {
		t.Fatalf("expected remaining queue context, got %#v", byChat)
	}
}

func TestWeWorkLongConnStateResolveChatType(t *testing.T) {
	state := newWeWorkLongConnState()
	state.storeReplyContext(&weworkReplyContext{
		MessageID: "msg-1",
		ReqID:     "req-1",
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    "chat-1",
		ChatType:  "group",
	})

	if got := state.resolveChatType("chat-1", nil); got != 2 {
		t.Fatalf("expected cached group chat_type=2, got %d", got)
	}

	if got := state.resolveChatType("chat-1", map[string]interface{}{"chat_type": "single"}); got != 1 {
		t.Fatalf("expected metadata override chat_type=1, got %d", got)
	}
}

type testHijackResponseWriter struct {
	header http.Header
	conn   net.Conn
	brw    *bufio.ReadWriter
}

func (w *testHijackResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *testHijackResponseWriter) Write(p []byte) (int, error) {
	return w.brw.Write(p)
}

func (w *testHijackResponseWriter) WriteHeader(statusCode int) {}

func (w *testHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.brw, nil
}

func TestWeWorkRunLongConnSessionReceivesSubscribeAck(t *testing.T) {
	subscribeSeen := make(chan map[string]interface{}, 1)
	serverErr := make(chan error, 1)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		brw := bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn))
		req, err := http.ReadRequest(brw.Reader)
		if err != nil {
			serverErr <- err
			return
		}

		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(&testHijackResponseWriter{
			header: make(http.Header),
			conn:   serverConn,
			brw:    brw,
		}, req, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var payload map[string]interface{}
		if err := conn.ReadJSON(&payload); err != nil {
			serverErr <- err
			return
		}
		subscribeSeen <- payload

		headers, _ := payload["headers"].(map[string]interface{})
		reqID, _ := headers["req_id"].(string)
		if err := conn.WriteJSON(map[string]interface{}{
			"headers": map[string]string{
				"req_id": reqID,
			},
			"errcode": 0,
			"errmsg":  "ok",
		}); err != nil {
			serverErr <- err
			return
		}

		serverErr <- nil
	}()

	wsConn, _, err := websocket.NewClient(clientConn, &url.URL{
		Scheme: "ws",
		Host:   "example.com",
		Path:   "/",
	}, nil, 1024, 1024)
	if err != nil {
		t.Fatalf("websocket.NewClient error: %v", err)
	}
	defer wsConn.Close()

	channel, err := NewWeWorkChannel("bot1", config.WeWorkChannelConfig{
		Enabled:   true,
		Mode:      "websocket",
		BotID:     "bot-id-1",
		BotSecret: "bot-secret-1",
	}, bus.NewMessageBus(16))
	if err != nil {
		t.Fatalf("NewWeWorkChannel error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- channel.runLongConnSessionWithConn(ctx, wsConn)
	}()

	var payload map[string]interface{}
	select {
	case payload = <-subscribeSeen:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for subscribe payload")
	}

	rawCmd, ok := payload["cmd"].(string)
	if !ok || rawCmd != "aibot_subscribe" {
		encoded, _ := json.Marshal(payload)
		t.Fatalf("expected aibot_subscribe payload, got %s", string(encoded))
	}

	body, ok := payload["body"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected subscribe body, got %#v", payload["body"])
	}
	if got, _ := body["bot_id"].(string); got != "bot-id-1" {
		t.Fatalf("expected bot_id bot-id-1, got %q", got)
	}
	if got, _ := body["secret"].(string); got != "bot-secret-1" {
		t.Fatalf("expected secret bot-secret-1, got %q", got)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runLongConnSession error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting runLongConnSession exit")
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting server exit")
	}
}
