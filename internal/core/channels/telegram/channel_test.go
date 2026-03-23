package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	telegrambot "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
)

type telegramUploadCapture struct {
	Fields    map[string]string
	FileField string
	FileName  string
	FileBytes []byte
}

type telegramRoundTripFunc func(*http.Request) (*http.Response, error)

func (f telegramRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestTelegramSendRemotePhotoUsesMultipartUpload(t *testing.T) {
	photoBytes := []byte("fake-jpeg-data")
	photoURL := "https://assets.test/photo.jpg"
	var capture telegramUploadCapture

	client := &http.Client{
		Transport: telegramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.String() {
			case "https://telegram.test/botTEST_TOKEN/getMe":
				return newTelegramHTTPResponse(r, http.StatusOK, "application/json", []byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"bot"}}`)), nil
			case "https://telegram.test/botTEST_TOKEN/sendChatAction":
				return newTelegramHTTPResponse(r, http.StatusOK, "application/json", []byte(`{"ok":true,"result":true}`)), nil
			case photoURL:
				return newTelegramHTTPResponse(r, http.StatusOK, "image/jpeg", photoBytes), nil
			case "https://telegram.test/botTEST_TOKEN/sendPhoto":
				capture = parseTelegramMultipartRequest(t, r, "photo")
				return newTelegramHTTPResponse(r, http.StatusOK, "application/json", []byte(`{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":123,"type":"private"}}}`)), nil
			default:
				t.Fatalf("unexpected url: %s", r.URL.String())
				return nil, nil
			}
		}),
	}

	channel := newTestTelegramChannel(t, client)

	err := channel.Send(&bus.OutboundMessage{
		ChatID:    "123",
		Content:   "caption",
		Media:     []bus.Media{{Type: shared.UnifiedMediaImage, URL: photoURL, Name: "photo.jpg"}},
		ReplyTo:   "7",
		Channel:   "telegram",
		AccountID: "default",
	})
	if err != nil {
		t.Fatalf("Send returned err: %v", err)
	}

	if got := capture.Fields["chat_id"]; got != "123" {
		t.Fatalf("unexpected chat_id: %q", got)
	}
	if got := capture.Fields["caption"]; got != "caption" {
		t.Fatalf("unexpected caption: %q", got)
	}
	if got := capture.Fields["reply_to_message_id"]; got != "7" {
		t.Fatalf("unexpected reply_to_message_id: %q", got)
	}
	if capture.FileField != "photo" {
		t.Fatalf("unexpected file field: %q", capture.FileField)
	}
	if capture.FileName != "photo.jpg" {
		t.Fatalf("unexpected file name: %q", capture.FileName)
	}
	if !bytes.Equal(capture.FileBytes, photoBytes) {
		t.Fatalf("uploaded photo bytes mismatch")
	}
}

func TestTelegramSendRemoteDocumentUsesMultipartUpload(t *testing.T) {
	documentBytes := []byte("plain-text-document")
	documentURL := "https://assets.test/readme.txt"
	var capture telegramUploadCapture

	client := &http.Client{
		Transport: telegramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.String() {
			case "https://telegram.test/botTEST_TOKEN/getMe":
				return newTelegramHTTPResponse(r, http.StatusOK, "application/json", []byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"bot"}}`)), nil
			case "https://telegram.test/botTEST_TOKEN/sendChatAction":
				return newTelegramHTTPResponse(r, http.StatusOK, "application/json", []byte(`{"ok":true,"result":true}`)), nil
			case documentURL:
				return newTelegramHTTPResponse(r, http.StatusOK, "text/plain", documentBytes), nil
			case "https://telegram.test/botTEST_TOKEN/sendDocument":
				capture = parseTelegramMultipartRequest(t, r, "document")
				return newTelegramHTTPResponse(r, http.StatusOK, "application/json", []byte(`{"ok":true,"result":{"message_id":2,"date":1710000000,"chat":{"id":456,"type":"private"}}}`)), nil
			default:
				t.Fatalf("unexpected url: %s", r.URL.String())
				return nil, nil
			}
		}),
	}

	channel := newTestTelegramChannel(t, client)

	err := channel.Send(&bus.OutboundMessage{
		ChatID:    "456",
		Content:   "document",
		Media:     []bus.Media{{Type: shared.UnifiedMediaFile, URL: documentURL, Name: "readme.txt"}},
		Channel:   "telegram",
		AccountID: "default",
	})
	if err != nil {
		t.Fatalf("Send returned err: %v", err)
	}

	if got := capture.Fields["chat_id"]; got != "456" {
		t.Fatalf("unexpected chat_id: %q", got)
	}
	if got := capture.Fields["caption"]; got != "document" {
		t.Fatalf("unexpected caption: %q", got)
	}
	if capture.FileField != "document" {
		t.Fatalf("unexpected file field: %q", capture.FileField)
	}
	if capture.FileName != "readme.txt" {
		t.Fatalf("unexpected file name: %q", capture.FileName)
	}
	if !bytes.Equal(capture.FileBytes, documentBytes) {
		t.Fatalf("uploaded document bytes mismatch")
	}
}

func TestTelegramMaterializeUploadDataRejectsOversizedPhoto(t *testing.T) {
	channel := &TelegramChannel{}
	raw := bytes.Repeat([]byte("a"), (10<<20)+1)

	_, err := channel.materializeTelegramUploadData(bus.Media{
		Type:   shared.UnifiedMediaImage,
		Base64: base64.StdEncoding.EncodeToString(raw),
	}, 10<<20)
	if err == nil {
		t.Fatalf("expected size limit error")
	}
	if !strings.Contains(err.Error(), "media exceeds size limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestTelegramChannel(t *testing.T, client *http.Client) *TelegramChannel {
	t.Helper()

	bot, err := telegrambot.NewBotAPIWithClient("TEST_TOKEN", "https://telegram.test/bot%s/%s", client)
	if err != nil {
		t.Fatalf("NewBotAPIWithClient returned err: %v", err)
	}

	base := shared.NewBaseChannelImpl("telegram", "default", shared.BaseChannelConfig{
		Enabled: true,
	}, bus.NewMessageBus(1))
	_ = base.Start(context.Background())

	return &TelegramChannel{
		BaseChannelImpl: base,
		bot:             bot,
		token:           "TEST_TOKEN",
	}
}

func parseTelegramMultipartRequest(t *testing.T, r *http.Request, fileField string) telegramUploadCapture {
	t.Helper()

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data;") {
		t.Fatalf("expected multipart/form-data content type, got %q", contentType)
	}

	reader, err := r.MultipartReader()
	if err != nil {
		t.Fatalf("MultipartReader returned err: %v", err)
	}

	capture := telegramUploadCapture{Fields: map[string]string{}}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart returned err: %v", err)
		}

		body, readErr := io.ReadAll(part)
		if readErr != nil {
			t.Fatalf("ReadAll returned err: %v", readErr)
		}

		if part.FormName() == fileField {
			capture.FileField = part.FormName()
			capture.FileName = part.FileName()
			capture.FileBytes = body
			continue
		}

		capture.Fields[part.FormName()] = string(body)
	}

	return capture
}

func newTelegramHTTPResponse(req *http.Request, statusCode int, contentType string, body []byte) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}
}
