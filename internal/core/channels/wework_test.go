package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/config"
)

type weworkWebhookRoundTripFunc func(*http.Request) (*http.Response, error)

func (f weworkWebhookRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestExtractWeWorkEncryptSupportsXML(t *testing.T) {
	body := []byte(`<xml><ToUserName><![CDATA[toUser]]></ToUserName><Encrypt><![CDATA[ciphertext]]></Encrypt></xml>`)
	if got := extractWeWorkEncrypt(body); got != "ciphertext" {
		t.Fatalf("expected ciphertext, got %q", got)
	}
}

func encryptWeWorkWebhookMessage(t *testing.T, plaintext []byte, encodingAESKey, corpID string) string {
	t.Helper()

	key, err := decodeWeWorkAESKey(encodingAESKey)
	if err != nil {
		t.Fatalf("decode aes key: %v", err)
	}

	body := make([]byte, 0, 16+4+len(plaintext)+len(corpID)+weworkPKCS7BlockSize)
	body = append(body, []byte("1234567890abcdef")...)

	var msgLen [4]byte
	binary.BigEndian.PutUint32(msgLen[:], uint32(len(plaintext)))
	body = append(body, msgLen[:]...)
	body = append(body, plaintext...)
	body = append(body, []byte(corpID)...)

	padding := weworkPKCS7BlockSize - (len(body) % weworkPKCS7BlockSize)
	if padding == 0 {
		padding = weworkPKCS7BlockSize
	}
	body = append(body, bytes.Repeat([]byte{byte(padding)}, padding)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new aes cipher: %v", err)
	}

	ciphertext := make([]byte, len(body))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, body)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func TestWeWorkHandleWebhookImageMessageUsesMediaID(t *testing.T) {
	messageBus := bus.NewMessageBus(4)
	encodingAESKey := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	channel, err := NewWeWorkChannel("bot1", config.WeWorkChannelConfig{
		Enabled:        true,
		Mode:           "webhook",
		CorpID:         "corp-id-1",
		Secret:         "corp-secret-1",
		AgentID:        "1000002",
		Token:          "",
		EncodingAESKey: encodingAESKey,
		WebhookPort:    8766,
	}, messageBus)
	if err != nil {
		t.Fatalf("NewWeWorkChannel error: %v", err)
	}

	var getTokenCalls int
	var mediaGetCalls int
	channel.httpClient = &http.Client{
		Transport: weworkWebhookRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/cgi-bin/gettoken"):
				getTokenCalls++
				body := `{"errcode":0,"errmsg":"ok","access_token":"access-token-1","expires_in":7200}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			case strings.Contains(req.URL.Path, "/cgi-bin/media/get"):
				mediaGetCalls++
				if req.URL.Query().Get("access_token") != "access-token-1" {
					t.Fatalf("unexpected access_token: %q", req.URL.Query().Get("access_token"))
				}
				if req.URL.Query().Get("media_id") != "media-id-123" {
					t.Fatalf("unexpected media_id: %q", req.URL.Query().Get("media_id"))
				}
				header := make(http.Header)
				header.Set("Content-Type", "image/png")
				header.Set("Content-Disposition", `attachment; filename="callback-image.png"`)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(testWeWorkPNGData)),
					Header:     header,
					Request:    req,
				}, nil
			default:
				t.Fatalf("unexpected request URL: %s", req.URL.String())
				return nil, nil
			}
		}),
		Timeout: 5 * time.Second,
	}

	plaintext := `<xml>
<ToUserName><![CDATA[toUser]]></ToUserName>
<FromUserName><![CDATA[user-1]]></FromUserName>
<CreateTime>1710000000</CreateTime>
<MsgType><![CDATA[image]]></MsgType>
<PicUrl><![CDATA[https://forbidden.example.com/image]]></PicUrl>
<MediaId><![CDATA[media-id-123]]></MediaId>
<MsgId>msg-1</MsgId>
<AgentID>1000002</AgentID>
</xml>`
	encrypted := encryptWeWorkWebhookMessage(t, []byte(plaintext), encodingAESKey, "corp-id-1")
	body := `<xml><Encrypt><![CDATA[` + encrypted + `]]></Encrypt></xml>`

	req := httptest.NewRequest(http.MethodPost, "/wework/event", strings.NewReader(body))
	rec := httptest.NewRecorder()
	channel.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if getTokenCalls != 1 {
		t.Fatalf("expected 1 gettoken call, got %d", getTokenCalls)
	}
	if mediaGetCalls != 1 {
		t.Fatalf("expected 1 media/get call, got %d", mediaGetCalls)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	inbound, err := messageBus.ConsumeInbound(ctx)
	if err != nil {
		t.Fatalf("ConsumeInbound error: %v", err)
	}
	if inbound.Content != "[图片]" {
		t.Fatalf("expected [图片], got %q", inbound.Content)
	}
	if len(inbound.Media) != 1 {
		t.Fatalf("expected 1 media item, got %d", len(inbound.Media))
	}
	if inbound.Media[0].Type != UnifiedMediaImage {
		t.Fatalf("expected image media type, got %q", inbound.Media[0].Type)
	}
	if inbound.Media[0].Name != "callback-image.png" {
		t.Fatalf("expected callback-image.png, got %q", inbound.Media[0].Name)
	}
	if inbound.Media[0].MimeType != "image/png" {
		t.Fatalf("expected image/png, got %q", inbound.Media[0].MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(inbound.Media[0].Base64)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if !bytes.Equal(decoded, testWeWorkPNGData) {
		t.Fatalf("downloaded image content mismatch")
	}
}
