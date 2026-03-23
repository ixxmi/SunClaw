package weixinbridge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionStartThenScan(t *testing.T) {
	server := NewServer(Config{})
	handler := server.Handler()

	startReq := httptest.NewRequest(http.MethodPost, "/session/start", nil)
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d", startRec.Code)
	}

	var started sessionState
	if err := json.Unmarshal(startRec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if started.SessionID == "" || !started.NeedsScan || started.Authenticated {
		t.Fatalf("unexpected start response: %+v", started)
	}

	body := bytes.NewBufferString(`{"session_id":"` + started.SessionID + `"}`)
	scanReq := httptest.NewRequest(http.MethodPost, "/session/scan", body)
	scanRec := httptest.NewRecorder()
	handler.ServeHTTP(scanRec, scanReq)
	if scanRec.Code != http.StatusOK {
		t.Fatalf("scan status = %d", scanRec.Code)
	}

	var scanned sessionState
	if err := json.Unmarshal(scanRec.Body.Bytes(), &scanned); err != nil {
		t.Fatalf("decode scan response: %v", err)
	}
	if !scanned.Authenticated || scanned.NeedsScan {
		t.Fatalf("unexpected scan response: %+v", scanned)
	}
}

func TestInjectMessagesThenDrain(t *testing.T) {
	server := NewServer(Config{})
	handler := server.Handler()

	injectReq := httptest.NewRequest(http.MethodPost, "/messages/inject", bytes.NewBufferString(`{"sender_id":"wx-user","chat_id":"wx-chat","content":"你好"}`))
	injectRec := httptest.NewRecorder()
	handler.ServeHTTP(injectRec, injectReq)
	if injectRec.Code != http.StatusOK {
		t.Fatalf("inject status = %d", injectRec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/messages", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("messages status = %d", getRec.Code)
	}

	var payload struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("message count = %d", len(payload.Messages))
	}

	getRec2 := httptest.NewRecorder()
	handler.ServeHTTP(getRec2, getReq)
	if err := json.Unmarshal(getRec2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode drained messages: %v", err)
	}
	if len(payload.Messages) != 0 {
		t.Fatalf("expected queue to drain, got %d", len(payload.Messages))
	}
}

func TestSendRequiresAuthentication(t *testing.T) {
	server := NewServer(Config{})
	handler := server.Handler()

	sendReq := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(`{"chat_id":"wx-chat","text":"hello"}`))
	sendRec := httptest.NewRecorder()
	handler.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusUnauthorized {
		t.Fatalf("send status = %d", sendRec.Code)
	}
}
