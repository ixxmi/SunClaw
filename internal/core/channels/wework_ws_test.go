package channels

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/config"
)

var testWeWorkPNGData = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
	0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
	0x44, 0xae, 0x42, 0x60, 0x82,
}

var testWeWorkGIFData = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00,
	0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00,
	0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x4c,
	0x01, 0x00, 0x3b,
}

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
	ackWritten := make(chan struct{})
	serverDone := make(chan struct{})
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

		close(ackWritten)
		<-serverDone
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
	headers, _ := payload["headers"].(map[string]interface{})
	reqID, _ := headers["req_id"].(string)

	select {
	case <-ackWritten:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for subscribe ack write")
	}
	waitForWeWorkAckResolved(t, channel.longConn, reqID)

	cancel()
	close(serverDone)

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

func TestWeWorkSendLongConnMessageUploadsImageReply(t *testing.T) {
	raw := testWeWorkPNGData
	sum := md5.Sum(raw)

	wsConn, serverErr := newTestWeWorkWebsocketConn(t, func(conn *websocket.Conn) error {
		frame, err := readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_init" {
			return fmt.Errorf("unexpected first command: %s", frame.Cmd)
		}
		var body struct {
			Type        string `json:"type"`
			Filename    string `json:"filename"`
			TotalSize   int    `json:"total_size"`
			TotalChunks int    `json:"total_chunks"`
			MD5         string `json:"md5"`
		}
		if err := json.Unmarshal(frame.Body, &body); err != nil {
			return err
		}
		if body.Type != UnifiedMediaImage {
			return fmt.Errorf("unexpected init type: %s", body.Type)
		}
		if body.Filename != "image.png" {
			return fmt.Errorf("unexpected init filename: %s", body.Filename)
		}
		if body.TotalSize != len(raw) {
			return fmt.Errorf("unexpected init total_size: %d", body.TotalSize)
		}
		if body.TotalChunks != 1 {
			return fmt.Errorf("unexpected init total_chunks: %d", body.TotalChunks)
		}
		if body.MD5 != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("unexpected init md5: %s", body.MD5)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, map[string]string{
			"upload_id": "upload-image-1",
		}); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_chunk" {
			return fmt.Errorf("unexpected second command: %s", frame.Cmd)
		}
		var chunkBody struct {
			UploadID   string `json:"upload_id"`
			ChunkIndex int    `json:"chunk_index"`
			Base64Data string `json:"base64_data"`
		}
		if err := json.Unmarshal(frame.Body, &chunkBody); err != nil {
			return err
		}
		if chunkBody.UploadID != "upload-image-1" {
			return fmt.Errorf("unexpected upload_id: %s", chunkBody.UploadID)
		}
		if chunkBody.ChunkIndex != 0 {
			return fmt.Errorf("unexpected chunk_index: %d", chunkBody.ChunkIndex)
		}
		if chunkBody.Base64Data != base64.StdEncoding.EncodeToString(raw) {
			return fmt.Errorf("unexpected chunk data")
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, nil); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_finish" {
			return fmt.Errorf("unexpected third command: %s", frame.Cmd)
		}
		var finishBody struct {
			UploadID string `json:"upload_id"`
		}
		if err := json.Unmarshal(frame.Body, &finishBody); err != nil {
			return err
		}
		if finishBody.UploadID != "upload-image-1" {
			return fmt.Errorf("unexpected finish upload_id: %s", finishBody.UploadID)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, map[string]string{
			"type":       UnifiedMediaImage,
			"media_id":   "media-image-1",
			"created_at": "1380000000",
		}); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_respond_msg" {
			return fmt.Errorf("unexpected fourth command: %s", frame.Cmd)
		}
		if frame.Headers.ReqID != "req-image-1" {
			return fmt.Errorf("unexpected reply req_id: %s", frame.Headers.ReqID)
		}
		var replyBody struct {
			MsgType string `json:"msgtype"`
			Image   struct {
				MediaID string `json:"media_id"`
			} `json:"image"`
		}
		if err := json.Unmarshal(frame.Body, &replyBody); err != nil {
			return err
		}
		if replyBody.MsgType != UnifiedMediaImage {
			return fmt.Errorf("unexpected reply msgtype: %s", replyBody.MsgType)
		}
		if replyBody.Image.MediaID != "media-image-1" {
			return fmt.Errorf("unexpected reply media_id: %s", replyBody.Image.MediaID)
		}
		return writeWeWorkTestAck(conn, frame.Headers.ReqID, nil)
	})
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
	channel.longConn.setConn(wsConn)
	stopReadLoop, readErrCh := startWeWorkTestReadLoop(channel, wsConn)
	defer stopReadLoop()
	defer assertWeWorkTestReadLoopErr(t, readErrCh)
	channel.longConn.storeReplyContext(&weworkReplyContext{
		MessageID: "msg-image-1",
		ReqID:     "req-image-1",
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    "chat-1",
		ChatType:  "group",
	})

	err = channel.sendLongConnMessage(&bus.OutboundMessage{
		ChatID:    "chat-1",
		ReplyTo:   "msg-image-1",
		Timestamp: time.Now(),
		Media: []bus.Media{
			{
				Type:   UnifiedMediaImage,
				Name:   "image.png",
				Base64: base64.StdEncoding.EncodeToString(raw),
			},
		},
	})
	if err != nil {
		t.Fatalf("sendLongConnMessage returned error: %v", err)
	}

	assertWeWorkTestServerErr(t, serverErr)
}

func TestWeWorkSendLongConnMessageSendsTextThenFileReply(t *testing.T) {
	raw := []byte("test-file-bytes")
	sum := md5.Sum(raw)

	wsConn, serverErr := newTestWeWorkWebsocketConn(t, func(conn *websocket.Conn) error {
		frame, err := readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_respond_msg" {
			return fmt.Errorf("unexpected first command: %s", frame.Cmd)
		}
		if frame.Headers.ReqID != "req-file-1" {
			return fmt.Errorf("unexpected text req_id: %s", frame.Headers.ReqID)
		}
		var textBody struct {
			MsgType  string `json:"msgtype"`
			Markdown struct {
				Content string `json:"content"`
			} `json:"markdown"`
		}
		if err := json.Unmarshal(frame.Body, &textBody); err != nil {
			return err
		}
		if textBody.MsgType != "markdown" {
			return fmt.Errorf("unexpected text msgtype: %s", textBody.MsgType)
		}
		if textBody.Markdown.Content != "请查收附件" {
			return fmt.Errorf("unexpected text content: %s", textBody.Markdown.Content)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, nil); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_init" {
			return fmt.Errorf("unexpected second command: %s", frame.Cmd)
		}
		var initBody struct {
			Type        string `json:"type"`
			Filename    string `json:"filename"`
			TotalSize   int    `json:"total_size"`
			TotalChunks int    `json:"total_chunks"`
			MD5         string `json:"md5"`
		}
		if err := json.Unmarshal(frame.Body, &initBody); err != nil {
			return err
		}
		if initBody.Type != UnifiedMediaFile {
			return fmt.Errorf("unexpected init type: %s", initBody.Type)
		}
		if initBody.Filename != "report.pdf" {
			return fmt.Errorf("unexpected init filename: %s", initBody.Filename)
		}
		if initBody.TotalSize != len(raw) {
			return fmt.Errorf("unexpected init total_size: %d", initBody.TotalSize)
		}
		if initBody.TotalChunks != 1 {
			return fmt.Errorf("unexpected init total_chunks: %d", initBody.TotalChunks)
		}
		if initBody.MD5 != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("unexpected init md5: %s", initBody.MD5)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, map[string]string{
			"upload_id": "upload-file-1",
		}); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_chunk" {
			return fmt.Errorf("unexpected third command: %s", frame.Cmd)
		}
		var chunkBody struct {
			UploadID   string `json:"upload_id"`
			ChunkIndex int    `json:"chunk_index"`
			Base64Data string `json:"base64_data"`
		}
		if err := json.Unmarshal(frame.Body, &chunkBody); err != nil {
			return err
		}
		if chunkBody.UploadID != "upload-file-1" {
			return fmt.Errorf("unexpected upload_id: %s", chunkBody.UploadID)
		}
		if chunkBody.ChunkIndex != 0 {
			return fmt.Errorf("unexpected chunk_index: %d", chunkBody.ChunkIndex)
		}
		if chunkBody.Base64Data != base64.StdEncoding.EncodeToString(raw) {
			return fmt.Errorf("unexpected chunk data")
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, nil); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_finish" {
			return fmt.Errorf("unexpected fourth command: %s", frame.Cmd)
		}
		var finishBody struct {
			UploadID string `json:"upload_id"`
		}
		if err := json.Unmarshal(frame.Body, &finishBody); err != nil {
			return err
		}
		if finishBody.UploadID != "upload-file-1" {
			return fmt.Errorf("unexpected finish upload_id: %s", finishBody.UploadID)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, map[string]string{
			"type":       UnifiedMediaFile,
			"media_id":   "media-file-1",
			"created_at": "1380000000",
		}); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_respond_msg" {
			return fmt.Errorf("unexpected fifth command: %s", frame.Cmd)
		}
		if frame.Headers.ReqID != "req-file-1" {
			return fmt.Errorf("unexpected file req_id: %s", frame.Headers.ReqID)
		}
		var replyBody struct {
			MsgType string `json:"msgtype"`
			File    struct {
				MediaID string `json:"media_id"`
			} `json:"file"`
		}
		if err := json.Unmarshal(frame.Body, &replyBody); err != nil {
			return err
		}
		if replyBody.MsgType != UnifiedMediaFile {
			return fmt.Errorf("unexpected file msgtype: %s", replyBody.MsgType)
		}
		if replyBody.File.MediaID != "media-file-1" {
			return fmt.Errorf("unexpected file media_id: %s", replyBody.File.MediaID)
		}
		return writeWeWorkTestAck(conn, frame.Headers.ReqID, nil)
	})
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
	channel.longConn.setConn(wsConn)
	stopReadLoop, readErrCh := startWeWorkTestReadLoop(channel, wsConn)
	defer stopReadLoop()
	defer assertWeWorkTestReadLoopErr(t, readErrCh)
	channel.longConn.storeReplyContext(&weworkReplyContext{
		MessageID: "msg-file-1",
		ReqID:     "req-file-1",
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    "chat-1",
		ChatType:  "group",
	})

	err = channel.sendLongConnMessage(&bus.OutboundMessage{
		ChatID:    "chat-1",
		ReplyTo:   "msg-file-1",
		Content:   "请查收附件",
		Timestamp: time.Now(),
		Media: []bus.Media{
			{
				Type:   UnifiedMediaFile,
				Name:   "report.pdf",
				Base64: base64.StdEncoding.EncodeToString(raw),
			},
		},
	})
	if err != nil {
		t.Fatalf("sendLongConnMessage returned error: %v", err)
	}

	assertWeWorkTestServerErr(t, serverErr)
}

func TestWeWorkUploadLongConnMediaRejectsUnsupportedImageFormat(t *testing.T) {
	convertedMedia, convertedData, err := normalizeWeWorkUploadImage(bus.Media{
		Type: UnifiedMediaImage,
		Name: "animated.gif",
	}, testWeWorkGIFData, weworkUploadImageMaxBytes)
	if err != nil {
		t.Fatalf("normalizeWeWorkUploadImage returned error: %v", err)
	}
	if got := strings.ToLower(http.DetectContentType(convertedData)); got != "image/png" {
		t.Fatalf("expected converted image/png, got %s", got)
	}
	if convertedMedia.Name != "animated.png" {
		t.Fatalf("expected converted filename animated.png, got %s", convertedMedia.Name)
	}
	if err := validateWeWorkUploadMedia(convertedMedia, convertedData); err != nil {
		t.Fatalf("validateWeWorkUploadMedia returned error: %v", err)
	}

	oversized := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			oversized.Set(x, y, color.RGBA{
				R: uint8((x * 17) % 256),
				G: uint8((y * 29) % 256),
				B: uint8(((x + y) * 13) % 256),
				A: 255,
			})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, oversized); err != nil {
		t.Fatalf("png.Encode error: %v", err)
	}
	shrunkMedia, shrunkData, err := normalizeWeWorkUploadImage(bus.Media{
		Type: UnifiedMediaImage,
		Name: "large.png",
	}, pngBuf.Bytes(), 1024)
	if err != nil {
		t.Fatalf("normalizeWeWorkUploadImage shrink returned error: %v", err)
	}
	if len(shrunkData) > 1024 {
		t.Fatalf("expected shrunk data <= 1024 bytes, got %d", len(shrunkData))
	}
	shrunkType := strings.ToLower(http.DetectContentType(shrunkData))
	if shrunkType != "image/png" && shrunkType != "image/jpeg" {
		t.Fatalf("unexpected shrunk content type: %s", shrunkType)
	}
	_ = shrunkMedia

	channel, err := NewWeWorkChannel("bot1", config.WeWorkChannelConfig{
		Enabled:   true,
		Mode:      "websocket",
		BotID:     "bot-id-1",
		BotSecret: "bot-secret-1",
	}, bus.NewMessageBus(16))
	if err != nil {
		t.Fatalf("NewWeWorkChannel error: %v", err)
	}

	_, err = channel.uploadLongConnMedia(context.Background(), bus.Media{
		Type:   UnifiedMediaImage,
		Name:   "invalid.gif",
		Base64: base64.StdEncoding.EncodeToString([]byte("GIF89a-invalid")),
	})
	if err == nil {
		t.Fatalf("expected unsupported image format error")
	}
	if !strings.Contains(err.Error(), "auto-conversion failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWeWorkUploadLongConnMediaRetriesWithSmallerImageOnInit40009(t *testing.T) {
	largeImageBase64 := buildLargeWeWorkTestPNGBase64(t, 1100, 1100)

	wsConn, serverErr := newTestWeWorkWebsocketConn(t, func(conn *websocket.Conn) error {
		frame, err := readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_init" {
			return fmt.Errorf("unexpected first command: %s", frame.Cmd)
		}
		var firstInit struct {
			Type        string `json:"type"`
			Filename    string `json:"filename"`
			TotalSize   int    `json:"total_size"`
			TotalChunks int    `json:"total_chunks"`
		}
		if err := json.Unmarshal(frame.Body, &firstInit); err != nil {
			return err
		}
		if firstInit.Type != UnifiedMediaImage {
			return fmt.Errorf("unexpected first init type: %s", firstInit.Type)
		}
		if firstInit.TotalSize <= weworkUploadImageSafeMaxBytes {
			return fmt.Errorf("expected first init size to exceed safe cap, got %d", firstInit.TotalSize)
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"headers": map[string]string{"req_id": frame.Headers.ReqID},
			"errcode": 40009,
			"errmsg":  "invalid image size",
		}); err != nil {
			return err
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_init" {
			return fmt.Errorf("unexpected second command: %s", frame.Cmd)
		}
		var secondInit struct {
			Type        string `json:"type"`
			Filename    string `json:"filename"`
			TotalSize   int    `json:"total_size"`
			TotalChunks int    `json:"total_chunks"`
		}
		if err := json.Unmarshal(frame.Body, &secondInit); err != nil {
			return err
		}
		if secondInit.TotalSize >= firstInit.TotalSize {
			return fmt.Errorf("expected second init size smaller than first, got first=%d second=%d", firstInit.TotalSize, secondInit.TotalSize)
		}
		if secondInit.TotalSize > weworkUploadImageSafeMaxBytes {
			return fmt.Errorf("expected second init size <= safe cap, got %d", secondInit.TotalSize)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, map[string]string{
			"upload_id": "upload-retry-1",
		}); err != nil {
			return err
		}

		for i := 0; i < secondInit.TotalChunks; i++ {
			frame, err = readWeWorkTestFrame(conn)
			if err != nil {
				return err
			}
			if frame.Cmd != "aibot_upload_media_chunk" {
				return fmt.Errorf("unexpected chunk command: %s", frame.Cmd)
			}
			var chunkBody struct {
				UploadID   string `json:"upload_id"`
				ChunkIndex int    `json:"chunk_index"`
			}
			if err := json.Unmarshal(frame.Body, &chunkBody); err != nil {
				return err
			}
			if chunkBody.UploadID != "upload-retry-1" {
				return fmt.Errorf("unexpected upload_id: %s", chunkBody.UploadID)
			}
			if chunkBody.ChunkIndex != i {
				return fmt.Errorf("unexpected chunk_index: %d want %d", chunkBody.ChunkIndex, i)
			}
			if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, nil); err != nil {
				return err
			}
		}

		frame, err = readWeWorkTestFrame(conn)
		if err != nil {
			return err
		}
		if frame.Cmd != "aibot_upload_media_finish" {
			return fmt.Errorf("unexpected finish command: %s", frame.Cmd)
		}
		if err := writeWeWorkTestAck(conn, frame.Headers.ReqID, map[string]string{
			"type":       UnifiedMediaImage,
			"media_id":   "media-retry-1",
			"created_at": "1380000000",
		}); err != nil {
			return err
		}
		return nil
	})
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
	channel.longConn.setConn(wsConn)
	stopReadLoop, readErrCh := startWeWorkTestReadLoop(channel, wsConn)
	defer stopReadLoop()
	defer assertWeWorkTestReadLoopErr(t, readErrCh)

	mediaID, err := channel.uploadLongConnMedia(context.Background(), bus.Media{
		Type:   UnifiedMediaImage,
		Name:   "large.png",
		Base64: largeImageBase64,
	})
	if err != nil {
		t.Fatalf("uploadLongConnMedia returned error: %v", err)
	}
	if mediaID != "media-retry-1" {
		t.Fatalf("unexpected mediaID: %s", mediaID)
	}

	assertWeWorkTestServerErr(t, serverErr)
}

func buildLargeWeWorkTestPNGBase64(t *testing.T, width, height int) string {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var seed uint32 = 0x12345678
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			img.Set(x, y, color.RGBA{
				R: uint8(seed >> 24),
				G: uint8(seed >> 16),
				B: uint8(seed >> 8),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode error: %v", err)
	}
	if buf.Len() <= weworkUploadImageSafeMaxBytes {
		t.Fatalf("expected generated PNG to exceed safe cap, got %d bytes", buf.Len())
	}
	if buf.Len() > weworkUploadImageSourceMaxBytes {
		t.Fatalf("generated PNG too large for source cap: %d bytes", buf.Len())
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func newTestWeWorkWebsocketConn(t *testing.T, handler func(*websocket.Conn) error) (*websocket.Conn, <-chan error) {
	t.Helper()

	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()

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

		serverErr <- handler(conn)
	}()

	wsConn, _, err := websocket.NewClient(clientConn, &url.URL{
		Scheme: "ws",
		Host:   "example.com",
		Path:   "/",
	}, nil, 1024, 1024)
	if err != nil {
		t.Fatalf("websocket.NewClient error: %v", err)
	}

	return wsConn, serverErr
}

func readWeWorkTestFrame(conn *websocket.Conn) (weworkLongConnFrame, error) {
	var frame weworkLongConnFrame
	err := conn.ReadJSON(&frame)
	return frame, err
}

func writeWeWorkTestAck(conn *websocket.Conn, reqID string, body interface{}) error {
	payload := map[string]interface{}{
		"headers": map[string]string{
			"req_id": reqID,
		},
		"errcode": 0,
		"errmsg":  "ok",
	}
	if body != nil {
		payload["body"] = body
	}
	return conn.WriteJSON(payload)
}

func assertWeWorkTestServerErr(t *testing.T, serverErr <-chan error) {
	t.Helper()

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting server exit")
	}
}

func startWeWorkTestReadLoop(channel *WeWorkChannel, conn *websocket.Conn) (func(), <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- channel.readLongConnLoop(ctx, conn)
	}()

	return func() {
		cancel()
		_ = conn.Close()
	}, errCh
}

func assertWeWorkTestReadLoopErr(t *testing.T, errCh <-chan error) {
	t.Helper()

	select {
	case err := <-errCh:
		if err == nil {
			return
		}
		if strings.Contains(err.Error(), "use of closed network connection") ||
			strings.Contains(err.Error(), "closed pipe") ||
			strings.Contains(err.Error(), "EOF") {
			return
		}
		t.Fatalf("read loop error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting read loop exit")
	}
}

func waitForWeWorkAckResolved(t *testing.T, state *weworkLongConnState, reqID string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state.ackMu.Lock()
		_, pending := state.pending[reqID]
		state.ackMu.Unlock()
		if !pending {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for ack resolution: %s", reqID)
}
