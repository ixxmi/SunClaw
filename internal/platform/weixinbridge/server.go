package weixinbridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
)

const defaultQRCodeTTL = 2 * time.Minute

// Config defines mock bridge runtime behavior.
type Config struct {
	Addr          string
	QRCodeTTL     time.Duration
	PublicBaseURL string
}

// Server is a reference/mock Weixin bridge implementation.
type Server struct {
	cfg Config

	mu      sync.Mutex
	session sessionState
	pending []bus.InboundMessage
	sent    []sentMessageRecord
}

type sessionState struct {
	Authenticated bool      `json:"authenticated"`
	NeedsScan     bool      `json:"needs_scan"`
	SessionID     string    `json:"session_id,omitempty"`
	QRCodeURL     string    `json:"qr_code_url,omitempty"`
	QRCodeBase64  string    `json:"qr_code_base64,omitempty"`
	ExpiresAt     int64     `json:"expires_at,omitempty"`
	Message       string    `json:"message,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type sentMessageRecord struct {
	ID        string      `json:"id"`
	ChatID    string      `json:"chat_id"`
	Text      string      `json:"text,omitempty"`
	Content   string      `json:"content,omitempty"`
	ReplyTo   string      `json:"reply_to,omitempty"`
	Media     []bus.Media `json:"media,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

type sendRequest struct {
	ChatID  string      `json:"chat_id"`
	Text    string      `json:"text,omitempty"`
	Content string      `json:"content,omitempty"`
	ReplyTo string      `json:"reply_to,omitempty"`
	Media   []bus.Media `json:"media,omitempty"`
}

type injectMessageRequest struct {
	ID        string                 `json:"id,omitempty"`
	SenderID  string                 `json:"sender_id,omitempty"`
	From      string                 `json:"from,omitempty"`
	ChatID    string                 `json:"chat_id,omitempty"`
	Content   string                 `json:"content,omitempty"`
	Text      string                 `json:"text,omitempty"`
	Type      string                 `json:"type,omitempty"`
	Media     []bus.Media            `json:"media,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp int64                  `json:"timestamp,omitempty"`
}

type scanRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

type stateResponse struct {
	Session sessionState         `json:"session"`
	Pending []bus.InboundMessage `json:"pending"`
	Sent    []sentMessageRecord  `json:"sent"`
}

// NewServer creates a mock Weixin bridge server.
func NewServer(cfg Config) *Server {
	if cfg.QRCodeTTL <= 0 {
		cfg.QRCodeTTL = defaultQRCodeTTL
	}
	return &Server{
		cfg:     cfg,
		pending: make([]bus.InboundMessage, 0),
		sent:    make([]sentMessageRecord, 0),
	}
}

// Handler returns the HTTP handler for the bridge.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/session/status", s.handleSessionStatus)
	mux.HandleFunc("/session/start", s.handleSessionStart)
	mux.HandleFunc("/session/qrcode", s.handleSessionQRCode)
	mux.HandleFunc("/session/scan", s.handleSessionScan)
	mux.HandleFunc("/session/reset", s.handleSessionReset)
	mux.HandleFunc("/messages", s.handleMessages)
	mux.HandleFunc("/messages/inject", s.handleInjectMessage)
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/sent", s.handleSent)
	mux.HandleFunc("/debug/state", s.handleDebugState)
	return mux
}

// Serve starts the bridge HTTP server.
func (s *Server) Serve(ctx context.Context) error {
	addr := strings.TrimSpace(s.cfg.Addr)
	if addr == "" {
		addr = "127.0.0.1:19090"
	}

	server := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"name": "weixin-bridge-mock",
	})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.currentSession())
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.startOrReuseSession())
}

func (s *Server) handleSessionScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var req scanRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	session, err := s.completeScan(req.SessionID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":      false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleSessionQRCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	session := s.currentSession()
	requestedSessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if session.SessionID == "" || (requestedSessionID != "" && requestedSessionID != session.SessionID) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if strings.TrimSpace(session.QRCodeBase64) == "" {
		http.Error(w, "qr code not available", http.StatusNotFound)
		return
	}

	data, err := base64.StdEncoding.DecodeString(session.QRCodeBase64)
	if err != nil {
		http.Error(w, "invalid qr code payload", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleSessionReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.resetSession())
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"messages": s.popMessages(),
		})
	default:
		writeMethodNotAllowed(w)
	}
}

func (s *Server) handleInjectMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var req injectMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":      false,
			"message": fmt.Sprintf("decode request failed: %v", err),
		})
		return
	}

	msg, err := buildInjectedMessage(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":      false,
			"message": err.Error(),
		})
		return
	}

	s.pushMessage(msg)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": msg,
	})
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	if !s.currentSession().Authenticated {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"ok":      false,
			"message": "weixin bridge mock is not authenticated",
		})
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":      false,
			"message": fmt.Sprintf("decode request failed: %v", err),
		})
		return
	}

	record, err := s.storeSent(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":      false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "message accepted",
		"record":  record,
	})
}

func (s *Server) handleSent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sent := make([]sentMessageRecord, len(s.sent))
	copy(sent, s.sent)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"messages": sent,
	})
}

func (s *Server) handleDebugState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	pending := make([]bus.InboundMessage, len(s.pending))
	copy(pending, s.pending)
	sent := make([]sentMessageRecord, len(s.sent))
	copy(sent, s.sent)

	writeJSON(w, http.StatusOK, stateResponse{
		Session: s.session,
		Pending: pending,
		Sent:    sent,
	})
}

func (s *Server) currentSession() sessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshSessionLocked()
	return s.session
}

func (s *Server) startOrReuseSession() sessionState {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refreshSessionLocked()
	if s.session.Authenticated {
		return s.session
	}
	if s.session.NeedsScan && s.session.SessionID != "" && (s.session.ExpiresAt == 0 || time.Now().Unix() < s.session.ExpiresAt) {
		return s.session
	}

	sessionID := generateID("wxsess")
	expiresAt := time.Now().Add(s.cfg.QRCodeTTL)
	s.session = sessionState{
		Authenticated: false,
		NeedsScan:     true,
		SessionID:     sessionID,
		QRCodeURL:     s.buildQRCodeURL(sessionID),
		QRCodeBase64:  buildMockQRCodeBase64(sessionID, expiresAt),
		ExpiresAt:     expiresAt.Unix(),
		Message:       "scan the QR code with the real Weixin bridge or simulate via POST /session/scan",
		UpdatedAt:     time.Now(),
	}
	return s.session
}

func (s *Server) completeScan(sessionID string) (sessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refreshSessionLocked()
	if s.session.SessionID == "" {
		return sessionState{}, fmt.Errorf("no pending weixin session")
	}
	if sessionID != "" && sessionID != s.session.SessionID {
		return sessionState{}, fmt.Errorf("session_id mismatch")
	}

	s.session.Authenticated = true
	s.session.NeedsScan = false
	s.session.QRCodeURL = ""
	s.session.QRCodeBase64 = ""
	s.session.ExpiresAt = 0
	s.session.Message = "authenticated"
	s.session.UpdatedAt = time.Now()
	return s.session, nil
}

func (s *Server) resetSession() sessionState {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.session = sessionState{
		Authenticated: false,
		NeedsScan:     false,
		Message:       "session reset",
		UpdatedAt:     time.Now(),
	}
	return s.session
}

func (s *Server) popMessages() []bus.InboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]bus.InboundMessage, len(s.pending))
	copy(out, s.pending)
	s.pending = s.pending[:0]
	return out
}

func (s *Server) pushMessage(msg bus.InboundMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, msg)
}

func (s *Server) storeSent(req sendRequest) (sentMessageRecord, error) {
	if strings.TrimSpace(req.ChatID) == "" {
		return sentMessageRecord{}, fmt.Errorf("chat_id is required")
	}
	if strings.TrimSpace(req.Text) == "" && strings.TrimSpace(req.Content) == "" && len(req.Media) == 0 {
		return sentMessageRecord{}, fmt.Errorf("text/content/media cannot all be empty")
	}

	record := sentMessageRecord{
		ID:        generateID("wxout"),
		ChatID:    strings.TrimSpace(req.ChatID),
		Text:      strings.TrimSpace(req.Text),
		Content:   strings.TrimSpace(req.Content),
		ReplyTo:   strings.TrimSpace(req.ReplyTo),
		Media:     req.Media,
		Timestamp: time.Now().Unix(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, record)
	return record, nil
}

func (s *Server) refreshSessionLocked() {
	if s.session.NeedsScan && s.session.ExpiresAt > 0 && time.Now().Unix() >= s.session.ExpiresAt {
		s.session.Authenticated = false
		s.session.NeedsScan = false
		s.session.SessionID = ""
		s.session.QRCodeURL = ""
		s.session.QRCodeBase64 = ""
		s.session.ExpiresAt = 0
		s.session.Message = "qr code expired"
		s.session.UpdatedAt = time.Now()
	}
}

func (s *Server) buildQRCodeURL(sessionID string) string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.PublicBaseURL), "/")
	if base == "" {
		return ""
	}
	return base + "/session/qrcode?session_id=" + sessionID
}

func buildInjectedMessage(req injectMessageRequest) (bus.InboundMessage, error) {
	senderID := strings.TrimSpace(req.SenderID)
	if senderID == "" {
		senderID = strings.TrimSpace(req.From)
	}
	chatID := strings.TrimSpace(req.ChatID)
	if chatID == "" {
		chatID = senderID
	}
	if senderID == "" {
		return bus.InboundMessage{}, fmt.Errorf("sender_id/from is required")
	}
	if chatID == "" {
		return bus.InboundMessage{}, fmt.Errorf("chat_id is required")
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = strings.TrimSpace(req.Text)
	}

	ts := req.Timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = generateID("wxim")
	}

	msgType := strings.TrimSpace(req.Type)
	if msgType == "" {
		if len(req.Media) > 0 {
			msgType = req.Media[0].Type
		} else {
			msgType = "text"
		}
	}

	return bus.InboundMessage{
		ID:        id,
		Channel:   "weixin",
		AccountID: "default",
		SenderID:  senderID,
		ChatID:    chatID,
		Content:   content,
		Media:     req.Media,
		Metadata: map[string]interface{}{
			"message_type": msgType,
			"metadata":     req.Metadata,
		},
		Timestamp: time.Unix(ts, 0),
	}, nil
}

func buildMockQRCodeBase64(sessionID string, expiresAt time.Time) string {
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512"><rect width="100%%" height="100%%" fill="#fffaf0"/><rect x="20" y="20" width="472" height="472" rx="28" fill="#111827"/><text x="256" y="170" text-anchor="middle" font-size="28" fill="#fef3c7" font-family="monospace">WEIXIN MOCK QR</text><text x="256" y="240" text-anchor="middle" font-size="18" fill="#e5e7eb" font-family="monospace">%s</text><text x="256" y="292" text-anchor="middle" font-size="16" fill="#d1d5db" font-family="monospace">POST /session/scan</text><text x="256" y="330" text-anchor="middle" font-size="16" fill="#d1d5db" font-family="monospace">Expires %s</text></svg>`,
		sessionID,
		expiresAt.Format(time.RFC3339),
	)
	return base64.StdEncoding.EncodeToString([]byte(svg))
}

func generateID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
		"ok":      false,
		"message": "method not allowed",
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
