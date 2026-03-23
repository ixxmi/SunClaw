package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
)

type weixinRoundTripFunc func(*http.Request) (*http.Response, error)

func (f weixinRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestWeixinEnsureSessionTriggersScanOnStart(t *testing.T) {
	var requestedPaths []string

	channel, messageBus := newTestWeixinChannel("https://weixin-bridge.test", &http.Client{
		Transport: weixinRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedPaths = append(requestedPaths, req.URL.Path)
			switch req.URL.Path {
			case "/session/status":
				return newWeixinHTTPResponse(req, http.StatusOK, `{"authenticated":false,"needs_scan":false}`), nil
			case "/session/start":
				return newWeixinHTTPResponse(req, http.StatusOK, `{"authenticated":false,"needs_scan":true,"session_id":"sess-1","qr_code_url":"https://qr.example.com/1"}`), nil
			default:
				t.Fatalf("unexpected path: %s", req.URL.Path)
				return nil, nil
			}
		}),
	})
	_ = messageBus

	if err := channel.ensureSession(context.Background(), true); err != nil {
		t.Fatalf("ensureSession returned err: %v", err)
	}

	got := strings.Join(requestedPaths, ",")
	if got != "/session/status,/session/start" {
		t.Fatalf("requested paths = %q", got)
	}
}

func TestWeixinFetchMessagesPublishesInbound(t *testing.T) {
	channel, messageBus := newTestWeixinChannel("https://weixin-bridge.test", &http.Client{
		Transport: weixinRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/messages":
				return newWeixinHTTPResponse(req, http.StatusOK, `{"messages":[{"id":"msg-1","sender_id":"wx-user","chat_id":"wx-chat","text":"你好","type":"text","timestamp":1710000000,"media":[{"type":"image","url":"https://cdn.example.com/a.png","name":"a.png","mimetype":"image/png"}]}]}`), nil
			default:
				t.Fatalf("unexpected path: %s", req.URL.Path)
				return nil, nil
			}
		}),
	})

	if err := channel.fetchMessages(context.Background()); err != nil {
		t.Fatalf("fetchMessages returned err: %v", err)
	}

	inbound, err := messageBus.ConsumeInbound(context.Background())
	if err != nil {
		t.Fatalf("ConsumeInbound returned err: %v", err)
	}
	if inbound.Channel != "weixin" {
		t.Fatalf("channel = %q, want weixin", inbound.Channel)
	}
	if inbound.AccountID != "default" {
		t.Fatalf("account_id = %q, want default", inbound.AccountID)
	}
	if inbound.SenderID != "wx-user" || inbound.ChatID != "wx-chat" {
		t.Fatalf("unexpected sender/chat: %q %q", inbound.SenderID, inbound.ChatID)
	}
	if inbound.Content != "你好" {
		t.Fatalf("content = %q, want 你好", inbound.Content)
	}
	if len(inbound.Media) != 1 || inbound.Media[0].Type != "image" {
		t.Fatalf("unexpected media: %+v", inbound.Media)
	}
}

func TestWeixinSendForwardsMediaPayload(t *testing.T) {
	var payload weixinSendRequest

	channel, _ := newTestWeixinChannel("https://weixin-bridge.test", &http.Client{
		Transport: weixinRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/send":
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				return newWeixinHTTPResponse(req, http.StatusOK, `{"ok":true}`), nil
			default:
				t.Fatalf("unexpected path: %s", req.URL.Path)
				return nil, nil
			}
		}),
	})

	if err := channel.Send(&bus.OutboundMessage{
		ChatID:    "wx-chat",
		Content:   "look",
		ReplyTo:   "msg-1",
		Media:     []bus.Media{{Type: "image", URL: "https://cdn.example.com/a.png", Name: "a.png"}},
		Channel:   "weixin",
		AccountID: "default",
	}); err != nil {
		t.Fatalf("Send returned err: %v", err)
	}

	if payload.ChatID != "wx-chat" || payload.Text != "look" || payload.ReplyTo != "msg-1" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(payload.Media) != 1 || payload.Media[0].URL != "https://cdn.example.com/a.png" {
		t.Fatalf("unexpected media payload: %+v", payload.Media)
	}
}

func TestWeixinDirectHandleInboundPublishesMessage(t *testing.T) {
	channel, messageBus := newTestWeixinDirectChannel(&http.Client{})

	channel.handleDirectInboundMessage(context.Background(), weixinDirectMessage{
		MessageID:    42,
		FromUserID:   "wx-user",
		ContextToken: "ctx-1",
		SessionID:    "sess-1",
		ItemList: []weixinDirectMessageItem{
			{
				Type: weixinDirectMessageItemTypeText,
				TextItem: &weixinDirectTextItem{
					Text: "你好 direct",
				},
			},
		},
	})

	inbound, err := messageBus.ConsumeInbound(context.Background())
	if err != nil {
		t.Fatalf("ConsumeInbound returned err: %v", err)
	}
	if inbound.SenderID != "wx-user" || inbound.ChatID != "wx-user" {
		t.Fatalf("unexpected sender/chat: %q %q", inbound.SenderID, inbound.ChatID)
	}
	if inbound.Content != "你好 direct" {
		t.Fatalf("content = %q, want 你好 direct", inbound.Content)
	}
	if got, ok := channel.contextTokens.Load("wx-user"); !ok || got.(string) != "ctx-1" {
		t.Fatalf("expected context token to be stored, got=%v ok=%v", got, ok)
	}
}

func TestWeixinDirectSendRequiresContextToken(t *testing.T) {
	channel, _ := newTestWeixinDirectChannel(&http.Client{})

	err := channel.Send(&bus.OutboundMessage{
		ChatID:    "wx-user",
		Content:   "hello",
		Channel:   "weixin",
		AccountID: "default",
	})
	if err == nil || !strings.Contains(err.Error(), "context token") {
		t.Fatalf("expected context token error, got %v", err)
	}
}

func TestWeixinDirectSendUsesIlinkAPI(t *testing.T) {
	var payload weixinDirectSendMessageReq

	channel, _ := newTestWeixinDirectChannel(&http.Client{
		Transport: weixinRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/ilink/bot/sendmessage" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if auth := req.Header.Get("Authorization"); auth != "Bearer bot-token" {
				t.Fatalf("authorization = %q", auth)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			return newWeixinHTTPResponse(req, http.StatusOK, `{"ret":0,"errcode":0}`), nil
		}),
	})
	channel.contextTokens.Store("wx-user", "ctx-1")

	if err := channel.Send(&bus.OutboundMessage{
		ChatID:    "wx-user",
		Content:   "hello direct",
		Channel:   "weixin",
		AccountID: "default",
	}); err != nil {
		t.Fatalf("Send returned err: %v", err)
	}

	if payload.Msg.ToUserID != "wx-user" {
		t.Fatalf("to_user_id = %q", payload.Msg.ToUserID)
	}
	if payload.Msg.ContextToken != "ctx-1" {
		t.Fatalf("context_token = %q", payload.Msg.ContextToken)
	}
	if len(payload.Msg.ItemList) != 1 || payload.Msg.ItemList[0].TextItem == nil || payload.Msg.ItemList[0].TextItem.Text != "hello direct" {
		t.Fatalf("unexpected item list: %+v", payload.Msg.ItemList)
	}
}

func newTestWeixinChannel(bridgeURL string, client *http.Client) (*WeixinChannel, *bus.MessageBus) {
	messageBus := bus.NewMessageBus(1)
	channel, _ := NewWeixinChannel("default", WeixinConfig{
		BaseChannelConfig: shared.BaseChannelConfig{
			Enabled:   true,
			AccountID: "default",
		},
		BridgeURL: bridgeURL,
	}, messageBus)
	channel.client = client
	_ = channel.BaseChannelImpl.Start(context.Background())
	return channel, messageBus
}

func newTestWeixinDirectChannel(client *http.Client) (*WeixinChannel, *bus.MessageBus) {
	messageBus := bus.NewMessageBus(1)
	channel, _ := NewWeixinChannel("default", WeixinConfig{
		BaseChannelConfig: shared.BaseChannelConfig{
			Enabled:   true,
			AccountID: "default",
		},
		Mode:    "direct",
		Token:   "bot-token",
		BaseURL: "https://ilinkai.weixin.qq.com/",
	}, messageBus)
	channel.client = client
	directAPI, _ := newWeixinDirectAPIClient(channel.baseURL, channel.token, client)
	channel.directAPI = directAPI
	_ = channel.BaseChannelImpl.Start(context.Background())
	return channel, messageBus
}

func newWeixinHTTPResponse(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    req,
	}
}
