package channels

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// WeWorkChannel 企业微信通道
type WeWorkChannel struct {
	*BaseChannelImpl
	corpID         string
	agentID        string
	secret         string
	mode           string
	botID          string
	botSecret      string
	webSocketURL   string
	token          string
	encodingAESKey string
	webhookPort    int
	recvMsg        bool // 是否使用加密模式

	accessToken      string
	tokenExpiresAt   int64
	mu               sync.Mutex
	httpClient       *http.Client
	responseURLCache map[string][]string // chatID -> response_url 队列（FIFO），避免并发覆盖
	longConn         *weworkLongConnState
}

// NewWeWorkChannel 创建企业微信通道
func NewWeWorkChannel(accountID string, cfg config.WeWorkChannelConfig, bus *bus.MessageBus) (*WeWorkChannel, error) {
	mode := normalizeWeWorkChannelMode(cfg.Mode)
	switch mode {
	case "websocket":
		if cfg.BotID == "" || cfg.BotSecret == "" {
			return nil, fmt.Errorf("wework bot_id and bot_secret are required in websocket mode")
		}
	case "webhook":
		if cfg.CorpID == "" || cfg.Secret == "" || cfg.AgentID == "" {
			return nil, fmt.Errorf("wework corp_id, secret and agent_id are required in webhook mode")
		}
	default:
		return nil, fmt.Errorf("unsupported wework mode: %s", cfg.Mode)
	}

	baseCfg := BaseChannelConfig{
		Enabled:    cfg.Enabled,
		AllowedIDs: cfg.AllowedIDs,
	}

	port := cfg.WebhookPort
	if port == 0 {
		port = 8766
	}

	return &WeWorkChannel{
		BaseChannelImpl: NewBaseChannelImpl("wework", accountID, baseCfg, bus),
		corpID:          cfg.CorpID,
		agentID:         cfg.AgentID,
		secret:          cfg.Secret,
		mode:            mode,
		botID:           cfg.BotID,
		botSecret:       cfg.BotSecret,
		webSocketURL:    resolveWeWorkWebSocketURL(cfg.WebSocketURL),
		token:           cfg.Token,
		encodingAESKey:  cfg.EncodingAESKey,
		webhookPort:     port,
		recvMsg:         cfg.EncodingAESKey != "", // 有加密密钥则使用加密模式
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		longConn: newWeWorkLongConnState(),
	}, nil
}

// Start 启动企业微信通道
func (c *WeWorkChannel) Start(ctx context.Context) error {
	if err := c.BaseChannelImpl.Start(ctx); err != nil {
		return err
	}

	logger.Info("Starting WeWork channel", zap.String("mode", c.mode))

	if c.mode == "websocket" {
		go c.startLongConnLoop(ctx)
		return nil
	}

	go c.startWebhookServer(ctx)

	return nil
}

func (c *WeWorkChannel) startWebhookServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wework/event", c.handleWebhook)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", c.webhookPort),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("WeWork webhook server started", zap.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("WeWork webhook server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	_ = server.Shutdown(ctx)
}

func (c *WeWorkChannel) handleWebhook(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	signature := query.Get("msg_signature")
	timestamp := query.Get("timestamp")
	nonce := query.Get("nonce")
	echostr := query.Get("echostr")

	if r.Method == http.MethodGet {
		// 验证回调 URL
		if !c.verifySignature(c.token, timestamp, nonce, echostr, signature) {
			logger.Warn("Invalid signature for GET",
				zap.String("expected", c.computeSignature(c.token, timestamp, nonce, echostr)))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// echostr 是加密字符串，解密后返回明文 msg 字段
		decrypted, err := c.decryptMsg(echostr)
		if err != nil {
			logger.Error("Failed to decrypt echostr", zap.Error(err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(decrypted)
		return
	}

	// 处理 POST 请求
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read body", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	logger.Debug("WeWork POST body received",
		zap.Int("body_len", len(body)),
		zap.String("body_preview", truncateWeWorkLog(string(body), 200)))

	trimmedBody := bytes.TrimSpace(body)
	if len(trimmedBody) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var plaintext []byte
	encrypt := extractWeWorkEncrypt(trimmedBody)
	if encrypt != "" {
		// 验签（使用 msg_signature 参数）
		if c.token != "" {
			if !c.verifySignature(c.token, timestamp, nonce, encrypt, signature) {
				logger.Warn("Invalid signature for POST",
					zap.String("received", signature),
					zap.String("expected", c.computeSignature(c.token, timestamp, nonce, encrypt)))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		plaintext, err = c.decryptMsg(encrypt)
		if err != nil {
			logger.Error("Failed to decrypt message", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logger.Debug("WeWork message decrypted",
			zap.String("plaintext_preview", truncateWeWorkLog(string(plaintext), 200)))
	} else {
		// AI Bot 明文 JSON，无需解密
		plaintext = trimmedBody
	}

	// AI Bot 消息（含 aibotid 字段）
	if isAiBotMsg(plaintext) {
		c.handleAiBotMessage(w, plaintext)
		return
	}

	inMsg, err := c.decodeWebhookInboundMessage(plaintext)
	if err != nil {
		logger.Error("Failed to decode WeWork webhook inbound message",
			zap.Error(err),
			zap.String("plaintext", truncateWeWorkLog(string(plaintext), 300)))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if inMsg != nil {
		if !c.IsAllowed(inMsg.SenderID) {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = c.PublishInbound(context.Background(), inMsg)
	}

	w.WriteHeader(http.StatusOK)
}

type weworkWebhookMessageXML struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
	AgentID      string   `xml:"AgentID"`
	PicURL       string   `xml:"PicUrl"`
	MediaID      string   `xml:"MediaId"`
	ThumbMediaID string   `xml:"ThumbMediaId"`
	Format       string   `xml:"Format"`
	Event        string   `xml:"Event"`
	EventKey     string   `xml:"EventKey"`
}

type weworkWebhookMessageJSON struct {
	ToUserName   string `json:"tousername"`
	FromUserName string `json:"fromusername"`
	CreateTime   int64  `json:"createtime"`
	MsgType      string `json:"msgtype"`
	Content      string `json:"content"`
	MsgID        string `json:"msgid"`
	AgentID      string `json:"agentid"`
	PicURL       string `json:"picurl"`
	MediaID      string `json:"mediaid"`
	ThumbMediaID string `json:"thumbmediaid"`
	Format       string `json:"format"`
	Event        string `json:"event"`
	EventKey     string `json:"eventkey"`
}

func (c *WeWorkChannel) decodeWebhookInboundMessage(plaintext []byte) (*bus.InboundMessage, error) {
	trimmed := bytes.TrimSpace(plaintext)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '<' {
		var msg weworkWebhookMessageXML
		if err := xml.Unmarshal(trimmed, &msg); err != nil {
			return nil, fmt.Errorf("xml unmarshal failed: %w", err)
		}
		return c.buildWebhookInboundMessage(msg.FromUserName, msg.CreateTime, msg.MsgType, msg.Content, msg.MsgID, msg.AgentID, msg.PicURL, msg.MediaID, msg.Format)
	}

	var msg weworkWebhookMessageJSON
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}
	return c.buildWebhookInboundMessage(msg.FromUserName, msg.CreateTime, msg.MsgType, msg.Content, msg.MsgID, msg.AgentID, msg.PicURL, msg.MediaID, msg.Format)
}

func (c *WeWorkChannel) buildWebhookInboundMessage(senderID string, createTime int64, msgType, content, msgID, agentID, picURL, mediaID, format string) (*bus.InboundMessage, error) {
	msgType = strings.TrimSpace(strings.ToLower(msgType))
	if senderID == "" {
		return nil, nil
	}

	inMsg := &bus.InboundMessage{
		ID:        msgID,
		Content:   content,
		SenderID:  senderID,
		ChatID:    senderID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		Timestamp: time.Unix(createTime, 0),
		Metadata: map[string]interface{}{
			"agent_id": agentID,
			"msg_type": msgType,
		},
	}
	if picURL != "" {
		inMsg.Metadata["pic_url"] = picURL
	}
	if mediaID != "" {
		inMsg.Metadata["media_id"] = mediaID
	}

	switch msgType {
	case "text":
		if strings.TrimSpace(content) == "" {
			return nil, nil
		}
	case "image":
		inMsg.Content = "[图片]"
		media, err := c.downloadWebhookMediaByID(UnifiedMediaImage, mediaID, weworkUploadImageMaxBytes)
		if err != nil {
			return nil, err
		}
		inMsg.Media = []bus.Media{media}
	case "file":
		inMsg.Content = "[文件]"
		media, err := c.downloadWebhookMediaByID(UnifiedMediaFile, mediaID, weworkUploadFileMaxBytes)
		if err != nil {
			return nil, err
		}
		inMsg.Media = []bus.Media{media}
	case "voice":
		inMsg.Content = "[语音]"
		media, err := c.downloadWebhookMediaByID(UnifiedMediaAudio, mediaID, 2<<20)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(format) != "" && media.MimeType == "" {
			media.MimeType = "audio/" + strings.ToLower(strings.TrimSpace(format))
		}
		inMsg.Media = []bus.Media{media}
	case "video":
		inMsg.Content = "[视频]"
		media, err := c.downloadWebhookMediaByID(UnifiedMediaVideo, mediaID, 10<<20)
		if err != nil {
			return nil, err
		}
		inMsg.Media = []bus.Media{media}
	case "event":
		return nil, nil
	default:
		return nil, nil
	}

	return inMsg, nil
}

func (c *WeWorkChannel) downloadWebhookMediaByID(kind, mediaID string, maxBytes int64) (bus.Media, error) {
	if strings.TrimSpace(mediaID) == "" {
		return bus.Media{}, fmt.Errorf("wework %s message missing media_id", kind)
	}

	token, err := c.getAccessToken()
	if err != nil {
		return bus.Media{}, err
	}

	downloadURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/media/get?access_token=%s&media_id=%s", url.QueryEscape(token), url.QueryEscape(mediaID))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, downloadURL, nil)
	if err != nil {
		return bus.Media{}, fmt.Errorf("build media request failed: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return bus.Media{}, fmt.Errorf("download media failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bus.Media{}, fmt.Errorf("download media failed with status %d", resp.StatusCode)
	}

	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return bus.Media{}, fmt.Errorf("read media body failed: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return bus.Media{}, fmt.Errorf("media exceeds size limit: %d > %d", len(data), maxBytes)
	}

	var apiErr struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.Unmarshal(data, &apiErr) == nil && (apiErr.ErrCode != 0 || apiErr.ErrMsg != "") {
		return bus.Media{}, fmt.Errorf("wework media get failed: %d %s", apiErr.ErrCode, apiErr.ErrMsg)
	}

	mimeType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = detectWeWorkMediaContentType(data)
	}

	name := filenameFromDisposition(resp.Header.Get("Content-Disposition"))
	if name == "" {
		name = inferWeWorkInboundMediaName(kind, mediaID, mimeType)
	}

	return bus.Media{
		Type:     kind,
		Name:     name,
		Base64:   base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}, nil
}

func filenameFromDisposition(disposition string) string {
	disposition = strings.TrimSpace(disposition)
	if disposition == "" {
		return ""
	}

	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(params["filename*"]); name != "" {
		return strings.TrimPrefix(name, "UTF-8''")
	}
	return strings.TrimSpace(params["filename"])
}

func (c *WeWorkChannel) decryptMsg(encrypted string) ([]byte, error) {
	// 企业微信消息加解密流程（参考官方 SDK）：
	// 1. Base64 解码密文
	// 2. AES-256-CBC 解密：key = Base64Decode(EncodingAESKey+"=")，IV = key 前 16 字节
	// 3. 去掉企业微信自定义 PKCS7 填充（block size = 32，不是 aes.BlockSize=16）
	// 4. 跳过前 16 字节随机字符串
	// 5. 读取 4 字节大端序消息长度
	// 6. 提取消息体，末尾剩余部分为 CorpID

	if c.encodingAESKey == "" {
		return nil, fmt.Errorf("encoding_aes_key not configured")
	}

	// Step 1: Base64 解码密文
	ciphertext, err := DecodeBase64Media(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	// Step 2-4: 解码 AES key、AES-CBC 解密并去掉企业微信 PKCS7 填充
	keyBytes, err := decodeWeWorkAESKey(c.encodingAESKey)
	if err != nil {
		return nil, fmt.Errorf("key decode failed: %w", err)
	}
	plaintext, err := decryptWeWorkCBCPayloadWithKey(ciphertext, keyBytes)
	if err != nil {
		return nil, err
	}

	// Step 5: 跳过前 16 字节随机字符串
	if len(plaintext) < 20 { // 16(random) + 4(length)
		return nil, fmt.Errorf("plaintext too short after unpadding: %d", len(plaintext))
	}
	content := plaintext[16:]

	// Step 6: 读取 4 字节大端序消息长度
	msgLen := int(content[0])<<24 | int(content[1])<<16 | int(content[2])<<8 | int(content[3])
	content = content[4:]

	if msgLen < 0 || msgLen > len(content) {
		return nil, fmt.Errorf("invalid message length: %d (remaining: %d)", msgLen, len(content))
	}

	// Step 7: 提取消息体，剩余部分为 CorpID（去掉空字节再比较）
	message := content[:msgLen]
	receivedCorpID := strings.TrimRight(string(content[msgLen:]), "\x00")

	if receivedCorpID != "" && receivedCorpID != c.corpID {
		return nil, fmt.Errorf("corp_id mismatch: expected %s, got %s", c.corpID, receivedCorpID)
	}

	return message, nil
}

func (c *WeWorkChannel) computeSignature(token, timestamp, nonce, data string) string {
	// 排序
	strs := []string{token, timestamp, nonce, data}
	sort.Strings(strs)

	// 拼接
	str := strings.Join(strs, "")

	// SHA1
	h := sha1.New()
	h.Write([]byte(str))
	bs := h.Sum(nil)
	return hex.EncodeToString(bs)
}

func (c *WeWorkChannel) verifySignature(token, timestamp, nonce, data, signature string) bool {
	expected := c.computeSignature(token, timestamp, nonce, data)
	if expected != signature {
		logger.Debug("Signature mismatch",
			zap.String("expected", expected),
			zap.String("received", signature))
		return false
	}
	return true
}

func (c *WeWorkChannel) getAccessToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Unix() < c.tokenExpiresAt {
		return c.accessToken, nil
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s", c.corpID, c.secret)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("http get failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("json decode failed: %w", err)
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf("wechat api error: %s", result.ErrMsg)
	}

	c.accessToken = result.AccessToken
	c.tokenExpiresAt = time.Now().Unix() + result.ExpiresIn - 200
	return c.accessToken, nil
}

// Send 发送消息
// 优先级：AI Bot response_url > 普通应用消息 API（access_token）
func (c *WeWorkChannel) Send(msg *bus.OutboundMessage) error {
	if c.mode == "websocket" {
		return c.sendLongConnMessage(msg)
	}

	content := AppendMediaURLsToContent(msg.Content, msg.Media, map[string]bool{
		UnifiedMediaImage: true,
		UnifiedMediaFile:  true,
		UnifiedMediaVideo: true,
		UnifiedMediaAudio: true,
	})

	// 检查是否有 AI Bot response_url 缓存（AI Bot 消息必须通过 response_url 回复）
	c.mu.Lock()
	var responseURL string
	if queue, ok := c.responseURLCache[msg.ChatID]; ok && len(queue) > 0 {
		responseURL = queue[0]
		queue = queue[1:]
		if len(queue) == 0 {
			delete(c.responseURLCache, msg.ChatID)
		} else {
			c.responseURLCache[msg.ChatID] = queue
		}
	}
	c.mu.Unlock()

	if responseURL != "" {
		logger.Debug("WeWork Send via AI Bot response_url",
			zap.String("chat_id", msg.ChatID),
			zap.String("response_url", truncateWeWorkLog(responseURL, 80)))
		if err := c.sendAiBotReply(responseURL, content); err != nil {
			logger.Error("Failed to send AI Bot reply", zap.Error(err))
			return err
		}
		logger.Info("WeWork AI Bot reply sent", zap.String("chat_id", msg.ChatID))
		return nil
	}

	// 普通应用消息：通过 access_token 发送
	token, err := c.getAccessToken()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=%s", token)

	payload := map[string]interface{}{
		"touser":  msg.ChatID,
		"msgtype": "text",
		"agentid": c.agentID,
		"text": map[string]string{
			"content": content,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json marshal failed: %w", err)
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http post failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("json decode failed: %w", err)
	}

	if result.ErrCode != 0 {
		return fmt.Errorf("failed to send message: %s", result.ErrMsg)
	}

	logger.Info("WeWork message sent",
		zap.String("chat_id", msg.ChatID),
		zap.Int("content_length", len(content)),
	)

	return nil
}

func normalizeWeWorkChannelMode(mode string) string {
	trimmed := strings.TrimSpace(strings.ToLower(mode))
	if trimmed == "" {
		return "webhook"
	}
	return trimmed
}

func resolveWeWorkWebSocketURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "wss://openws.work.weixin.qq.com"
	}
	return trimmed
}

// extractWeWorkEncrypt 已不再使用（统一改为 JSON 解析），保留备用
func extractWeWorkEncrypt(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '{' {
		var jsonMsg struct {
			Encrypt string `json:"encrypt"`
		}
		if err := json.Unmarshal(trimmed, &jsonMsg); err == nil {
			return jsonMsg.Encrypt
		}
	}
	if trimmed[0] == '<' {
		var xmlMsg struct {
			Encrypt string `xml:"Encrypt"`
		}
		if err := xml.Unmarshal(trimmed, &xmlMsg); err == nil {
			return xmlMsg.Encrypt
		}
	}
	return ""
}

// truncateWeWorkLog 截断日志内容，避免过长日志刷屏
func truncateWeWorkLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// ─────────────────────────────────────────────
// 企业微信 AI Bot 消息结构体（按官方文档定义）
// 文档：https://developer.work.weixin.qq.com/document/path/98989
// ─────────────────────────────────────────────

// weworkAiBotTextContent 文本结构体
type weworkAiBotTextContent struct {
	Content string `json:"content"`
}

// weworkAiBotImageContent 图片结构体
type weworkAiBotImageContent struct {
	URL    string `json:"url"`
	AESKey string `json:"aeskey,omitempty"`
}

// weworkAiBotVoiceContent 语音结构体（已转成文本）
type weworkAiBotVoiceContent struct {
	Content string `json:"content"`
}

// weworkAiBotFileContent 文件结构体
type weworkAiBotFileContent struct {
	URL    string `json:"url"`
	AESKey string `json:"aeskey,omitempty"`
}

// weworkAiBotVideoContent 视频结构体
type weworkAiBotVideoContent struct {
	URL    string `json:"url"`
	AESKey string `json:"aeskey,omitempty"`
}

// weworkAiBotMixedItem 图文混排中的单条消息
type weworkAiBotMixedItem struct {
	MsgType string                   `json:"msgtype"`
	Text    *weworkAiBotTextContent  `json:"text,omitempty"`
	Image   *weworkAiBotImageContent `json:"image,omitempty"`
}

// weworkAiBotMixedContent 图文混排结构体
type weworkAiBotMixedContent struct {
	MsgItems []weworkAiBotMixedItem `json:"msg_item"`
}

// weworkAiBotStreamContent 流式消息刷新结构体
type weworkAiBotStreamContent struct {
	ID string `json:"id"`
}

// weworkAiBotQuote 引用结构体
type weworkAiBotQuote struct {
	MsgType string                   `json:"msgtype"`
	Text    *weworkAiBotTextContent  `json:"text,omitempty"`
	Image   *weworkAiBotImageContent `json:"image,omitempty"`
	Mixed   *weworkAiBotMixedContent `json:"mixed,omitempty"`
	Voice   *weworkAiBotVoiceContent `json:"voice,omitempty"`
	File    *weworkAiBotFileContent  `json:"file,omitempty"`
	Video   *weworkAiBotVideoContent `json:"video,omitempty"`
}

// weworkAiBotMessage 企业微信 AI Bot 消息（完整结构，覆盖全部消息类型）
//
// msgtype 取值：text / image / mixed / voice / file / video / stream
type weworkAiBotMessage struct {
	CreateTime  int64  `json:"create_time,omitempty"`
	MsgID       string `json:"msgid"`
	AiBotID     string `json:"aibotid"`
	ChatID      string `json:"chatid"`   // 仅群聊时存在
	ChatType    string `json:"chattype"` // single / group
	ResponseURL string `json:"response_url"`
	MsgType     string `json:"msgtype"`

	From struct {
		UserID string `json:"userid"`
	} `json:"from"`

	// 各消息类型 payload（按 msgtype 只有一个有值）
	Text   *weworkAiBotTextContent   `json:"text,omitempty"`
	Image  *weworkAiBotImageContent  `json:"image,omitempty"`
	Mixed  *weworkAiBotMixedContent  `json:"mixed,omitempty"`
	Voice  *weworkAiBotVoiceContent  `json:"voice,omitempty"`
	File   *weworkAiBotFileContent   `json:"file,omitempty"`
	Video  *weworkAiBotVideoContent  `json:"video,omitempty"`
	Stream *weworkAiBotStreamContent `json:"stream,omitempty"`
	Event  map[string]interface{}    `json:"event,omitempty"`

	// 引用消息（可选，text/mixed 消息时可能附带）
	Quote *weworkAiBotQuote `json:"quote,omitempty"`
}

// extractAiBotTextContent 从 AI Bot 消息中提取可供 LLM 处理的文本内容
// 对于非文本类型，拼接为可读描述；群聊时自动去掉 @机器人 前缀
func extractAiBotTextContent(msg *weworkAiBotMessage) string {
	var content string

	switch msg.MsgType {
	case "text":
		if msg.Text != nil {
			content = msg.Text.Content
		}
	case "voice":
		if msg.Voice != nil {
			content = msg.Voice.Content
		}
	case "image":
		if msg.Image != nil {
			content = "[图片] " + msg.Image.URL
		}
	case "file":
		if msg.File != nil {
			content = "[文件] " + msg.File.URL
		}
	case "video":
		if msg.Video != nil {
			content = "[视频] " + msg.Video.URL
		}
	case "mixed":
		if msg.Mixed != nil {
			var parts []string
			for _, item := range msg.Mixed.MsgItems {
				switch item.MsgType {
				case "text":
					if item.Text != nil {
						parts = append(parts, item.Text.Content)
					}
				case "image":
					if item.Image != nil {
						parts = append(parts, "[图片] "+item.Image.URL)
					}
				}
			}
			content = strings.Join(parts, "\n")
		}
	case "stream":
		// 流式刷新事件，无用户文本内容
		return ""
	}

	// 附加引用内容（让 LLM 了解上下文）
	if msg.Quote != nil {
		var quoteParts []string
		switch msg.Quote.MsgType {
		case "text":
			if msg.Quote.Text != nil {
				quoteParts = append(quoteParts, msg.Quote.Text.Content)
			}
		case "voice":
			if msg.Quote.Voice != nil {
				quoteParts = append(quoteParts, msg.Quote.Voice.Content)
			}
		case "image":
			if msg.Quote.Image != nil {
				quoteParts = append(quoteParts, "[引用图片] "+msg.Quote.Image.URL)
			}
		case "file":
			if msg.Quote.File != nil {
				quoteParts = append(quoteParts, "[引用文件] "+msg.Quote.File.URL)
			}
		case "mixed":
			if msg.Quote.Mixed != nil {
				for _, item := range msg.Quote.Mixed.MsgItems {
					if item.MsgType == "text" && item.Text != nil {
						quoteParts = append(quoteParts, item.Text.Content)
					}
				}
			}
		}
		if len(quoteParts) > 0 {
			content = "[引用: " + strings.Join(quoteParts, " ") + "]\n" + content
		}
	}

	return strings.TrimSpace(removeAtMention(content, msg.ChatType))
}

// removeAtMention 去掉群聊中 @机器人 的前缀
// 企业微信群聊消息格式：@机器人名称 实际内容，需要去掉 @ 前缀让后续逻辑正确识别指令
func removeAtMention(content, chatType string) string {
	if chatType != "group" {
		return content
	}
	// 去掉开头的 "@xxx " 格式（@后跟任意非空格字符，再跟空格）
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "@") {
		// 找到第一个空格或换行，去掉 @xxx 部分
		idx := strings.IndexAny(trimmed, " \t\n")
		if idx > 0 {
			trimmed = strings.TrimSpace(trimmed[idx+1:])
		}
	}
	return trimmed
}

// isAiBotMsg 判断 body 是否为企业微信 AI Bot 明文 JSON 消息（含 aibotid 字段）
func isAiBotMsg(body []byte) bool {
	return bytes.Contains(body, []byte(`"aibotid"`))
}

// handleAiBotMessage 处理企业微信 AI Bot 消息
func (c *WeWorkChannel) handleAiBotMessage(w http.ResponseWriter, body []byte) {
	var aibotMsg weworkAiBotMessage
	if err := json.Unmarshal(body, &aibotMsg); err != nil {
		logger.Error("Failed to unmarshal AI Bot message",
			zap.Error(err),
			zap.String("body", truncateWeWorkLog(string(body), 300)))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	logger.Debug("WeWork AI Bot message received",
		zap.String("msgid", aibotMsg.MsgID),
		zap.String("from", aibotMsg.From.UserID),
		zap.String("chattype", aibotMsg.ChatType),
		zap.String("msgtype", aibotMsg.MsgType))

	// 流式刷新事件：目前不处理，直接 200
	if aibotMsg.MsgType == "stream" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 提取文本内容
	content := extractAiBotTextContent(&aibotMsg)
	if content == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 权限校验
	if !c.IsAllowed(aibotMsg.From.UserID) {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 群聊用 chatID，单聊用 userID
	chatID := aibotMsg.From.UserID
	if aibotMsg.ChatType == "group" && aibotMsg.ChatID != "" {
		chatID = aibotMsg.ChatID
	}

	// 缓存 response_url（Send 时按 FIFO 取出使用，每个 URL 只消费一次）
	c.mu.Lock()
	if c.responseURLCache == nil {
		c.responseURLCache = make(map[string][]string)
	}
	c.responseURLCache[chatID] = append(c.responseURLCache[chatID], aibotMsg.ResponseURL)
	c.mu.Unlock()

	inMsg := &bus.InboundMessage{
		ID:        aibotMsg.MsgID,
		Content:   content,
		SenderID:  aibotMsg.From.UserID,
		ChatID:    chatID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"aibot_response_url": aibotMsg.ResponseURL,
			"aibot_id":           aibotMsg.AiBotID,
			"chat_type":          aibotMsg.ChatType,
			"msg_type":           aibotMsg.MsgType,
		},
	}

	_ = c.PublishInbound(context.Background(), inMsg)

	// 企业微信要求 5 秒内响应 200
	w.WriteHeader(http.StatusOK)
}

// sendAiBotReply 通过 response_url 主动回复 AI Bot 消息
// 文档：https://developer.work.weixin.qq.com/document/path/98989（主动回复消息）
// response_url 是一次性临时 URL（有效期1小时），直接 POST 明文 JSON，无需加密、无需 access_token
// 支持的消息类型：markdown / template_card
func (c *WeWorkChannel) sendAiBotReply(responseURL, content string) error {
	// 使用 markdown 格式回复（比 text 支持更丰富的格式，且官方主动回复文档推荐）
	payload := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json marshal failed: %w", err)
	}

	logger.Debug("WeWork AI Bot reply",
		zap.String("response_url", truncateWeWorkLog(responseURL, 80)),
		zap.String("content_preview", truncateWeWorkLog(content, 100)))

	resp, err := c.httpClient.Post(responseURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http post to response_url failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	logger.Debug("WeWork AI Bot reply response", zap.String("resp", truncateWeWorkLog(string(respBody), 200)))

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("json decode response failed: %w, raw: %s", err, truncateWeWorkLog(string(respBody), 200))
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("AI Bot reply failed: %s (errcode=%d)", result.ErrMsg, result.ErrCode)
	}
	return nil
}
