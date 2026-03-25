package weixin

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/channels/shared"
	"github.com/smallnest/goclaw/internal/core/config"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const (
	weixinDirectDefaultCDNBaseURL          = "https://novac2c.cdn.weixin.qq.com/c2c"
	weixinDirectDefaultPollTimeoutMs       = 35_000
	weixinDirectRetryDelay                 = 2 * time.Second
	weixinDirectBackoffDelay               = 30 * time.Second
	weixinDirectMaxConsecutiveFails        = 3
	weixinDirectSessionPauseDuration       = time.Hour
	weixinDirectSessionExpiredCode         = -14
	weixinDirectUploadRetryMax             = 3
	weixinDirectMediaMaxBytes        int64 = 20 << 20
)

func (c *WeixinChannel) startDirect(ctx context.Context) error {
	if c.directAPI == nil {
		return fmt.Errorf("weixin direct client is not configured")
	}

	logger.Info("Starting Weixin channel",
		zap.String("account_id", c.AccountID()),
		zap.String("mode", c.mode),
		zap.String("base_url", c.directAPI.BaseURL),
	)

	directCtx, cancel := context.WithCancel(ctx)
	c.directCancel = cancel

	go c.pollDirectMessages(directCtx)
	return nil
}

func (c *WeixinChannel) pollDirectMessages(ctx context.Context) {
	consecutiveFails := 0
	getUpdatesBuf, err := loadWeixinDirectCursor(c.syncCursorPath)
	if err != nil {
		logger.Warn("Failed to load Weixin cursor",
			zap.String("account_id", c.AccountID()),
			zap.String("path", c.syncCursorPath),
			zap.Error(err),
		)
		getUpdatesBuf = ""
	}

	nextTimeoutMs := weixinDirectDefaultPollTimeoutMs

	for {
		select {
		case <-ctx.Done():
			logger.Info("Weixin direct poll loop stopped",
				zap.String("account_id", c.AccountID()),
			)
			return
		case <-c.WaitForStop():
			logger.Info("Weixin direct channel stopped",
				zap.String("account_id", c.AccountID()),
			)
			return
		default:
		}

		if err := c.waitWhileWeixinDirectSessionPaused(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		pollCtx, cancel := context.WithTimeout(ctx, time.Duration(nextTimeoutMs+5000)*time.Millisecond)
		resp, err := c.directAPI.GetUpdates(pollCtx, weixinDirectGetUpdatesReq{
			GetUpdatesBuf: getUpdatesBuf,
		})
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveFails++
			logger.Warn("Weixin getUpdates failed",
				zap.String("account_id", c.AccountID()),
				zap.Int("attempt", consecutiveFails),
				zap.Error(err),
			)
			if consecutiveFails >= weixinDirectMaxConsecutiveFails {
				consecutiveFails = 0
				if !sleepWithContext(ctx, weixinDirectBackoffDelay) {
					return
				}
			} else if !sleepWithContext(ctx, weixinDirectRetryDelay) {
				return
			}
			continue
		}

		if isWeixinDirectSessionExpiredStatus(resp.Ret, resp.Errcode) {
			remaining := c.pauseWeixinDirectSession("getupdates", resp.Ret, resp.Errcode, resp.Errmsg)
			if !sleepWithContext(ctx, remaining) {
				return
			}
			continue
		}

		if resp.Errcode != 0 || resp.Ret != 0 {
			consecutiveFails++
			logger.Error("Weixin getUpdates API error",
				zap.String("account_id", c.AccountID()),
				zap.Int("ret", resp.Ret),
				zap.Int("errcode", resp.Errcode),
				zap.String("errmsg", resp.Errmsg),
			)
			if !sleepWithContext(ctx, weixinDirectRetryDelay) {
				return
			}
			continue
		}

		consecutiveFails = 0
		if resp.LongpollingTimeoutMs > 0 {
			nextTimeoutMs = resp.LongpollingTimeoutMs
		}

		if resp.GetUpdatesBuf != "" {
			getUpdatesBuf = resp.GetUpdatesBuf
			if err := saveWeixinDirectCursor(c.syncCursorPath, getUpdatesBuf); err != nil {
				logger.Warn("Failed to persist Weixin cursor",
					zap.String("account_id", c.AccountID()),
					zap.String("path", c.syncCursorPath),
					zap.Error(err),
				)
			}
		}

		for _, msg := range resp.Msgs {
			c.handleDirectInboundMessage(ctx, msg)
		}
	}
}

func (c *WeixinChannel) handleDirectInboundMessage(ctx context.Context, msg weixinDirectMessage) {
	fromUserID := strings.TrimSpace(msg.FromUserID)
	if fromUserID == "" {
		return
	}
	if !c.IsAllowed(fromUserID) {
		return
	}

	messageID := strings.TrimSpace(msg.ClientID)
	if messageID == "" && msg.MessageID > 0 {
		messageID = fmt.Sprintf("%d", msg.MessageID)
	}
	if messageID == "" {
		messageID = uuid.NewString()
	}

	var parts []string
	mediaList := make([]bus.Media, 0)
	for _, item := range msg.ItemList {
		c.collectWeixinDirectMessageItem(ctx, fromUserID, messageID, item, &parts, &mediaList)
	}

	content := strings.TrimSpace(strings.Join(parts, "\n"))
	if content == "" && len(mediaList) == 0 {
		return
	}

	if msg.ContextToken != "" {
		c.contextTokens.Store(fromUserID, msg.ContextToken)
	}

	inbound := &bus.InboundMessage{
		ID:        messageID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		SenderID:  fromUserID,
		ChatID:    fromUserID,
		Content:   content,
		Media:     mediaList,
		Metadata: map[string]interface{}{
			"context_token": msg.ContextToken,
			"session_id":    msg.SessionID,
			"message_id":    msg.MessageID,
		},
		Timestamp: time.Now(),
	}

	if err := c.PublishInbound(ctx, inbound); err != nil {
		logger.Error("Failed to publish Weixin inbound message",
			zap.String("account_id", c.AccountID()),
			zap.String("message_id", messageID),
			zap.Error(err),
		)
	}
}

func (c *WeixinChannel) collectWeixinDirectMessageItem(ctx context.Context, fromUserID, messageID string, item weixinDirectMessageItem, parts *[]string, mediaList *[]bus.Media) {
	switch item.Type {
	case weixinDirectMessageItemTypeText:
		if item.TextItem != nil && strings.TrimSpace(item.TextItem.Text) != "" {
			*parts = append(*parts, strings.TrimSpace(item.TextItem.Text))
		}
	case weixinDirectMessageItemTypeVoice:
		if item.VoiceItem != nil && strings.TrimSpace(item.VoiceItem.Text) != "" {
			*parts = append(*parts, strings.TrimSpace(item.VoiceItem.Text))
		} else {
			*parts = append(*parts, "[audio]")
		}
	case weixinDirectMessageItemTypeImage:
		*parts = append(*parts, "[image]")
	case weixinDirectMessageItemTypeFile:
		name := "file"
		if item.FileItem != nil && strings.TrimSpace(item.FileItem.FileName) != "" {
			name = strings.TrimSpace(item.FileItem.FileName)
		}
		*parts = append(*parts, fmt.Sprintf("[file: %s]", name))
	case weixinDirectMessageItemTypeVideo:
		*parts = append(*parts, "[video]")
	}

	if media, err := c.buildWeixinDirectInboundMedia(ctx, fromUserID, messageID, &item); err != nil {
		logger.Error("Failed to download Weixin inbound media",
			zap.String("account_id", c.AccountID()),
			zap.String("from_user_id", fromUserID),
			zap.String("message_id", messageID),
			zap.Int("message_item_type", item.Type),
			zap.Error(err),
		)
	} else if len(media) > 0 {
		*mediaList = append(*mediaList, media...)
	}

	if item.RefMsg != nil && item.RefMsg.MessageItem != nil {
		if item.Type == weixinDirectMessageItemTypeText || item.Type == 0 {
			if media, err := c.buildWeixinDirectInboundMedia(ctx, fromUserID, messageID, item.RefMsg.MessageItem); err == nil && len(media) > 0 {
				*mediaList = append(*mediaList, media...)
			}
		}
	}
}

func (c *WeixinChannel) buildWeixinDirectInboundMedia(ctx context.Context, fromUserID, messageID string, item *weixinDirectMessageItem) ([]bus.Media, error) {
	if item == nil {
		return nil, nil
	}

	switch item.Type {
	case weixinDirectMessageItemTypeImage:
		if item.ImageItem == nil {
			return nil, nil
		}
		ref := item.ImageItem.Media
		if ref == nil {
			ref = item.ImageItem.ThumbMedia
		}
		return c.downloadWeixinDirectInboundMedia(ctx, ref, shared.UnifiedMediaImage, "image", fromUserID, messageID)
	case weixinDirectMessageItemTypeVoice:
		if item.VoiceItem == nil {
			return nil, nil
		}
		return c.downloadWeixinDirectInboundMedia(ctx, item.VoiceItem.Media, shared.UnifiedMediaAudio, "voice.silk", fromUserID, messageID)
	case weixinDirectMessageItemTypeFile:
		if item.FileItem == nil {
			return nil, nil
		}
		name := strings.TrimSpace(item.FileItem.FileName)
		if name == "" {
			name = "file.bin"
		}
		return c.downloadWeixinDirectInboundMedia(ctx, item.FileItem.Media, shared.UnifiedMediaFile, name, fromUserID, messageID)
	case weixinDirectMessageItemTypeVideo:
		if item.VideoItem == nil {
			return nil, nil
		}
		return c.downloadWeixinDirectInboundMedia(ctx, item.VideoItem.Media, shared.UnifiedMediaVideo, "video.mp4", fromUserID, messageID)
	default:
		return nil, nil
	}
}

func (c *WeixinChannel) downloadWeixinDirectInboundMedia(ctx context.Context, ref *weixinDirectCDNMedia, mediaType, fallbackName, fromUserID, messageID string) ([]bus.Media, error) {
	if ref == nil || strings.TrimSpace(ref.EncryptQueryParam) == "" || strings.TrimSpace(ref.AesKey) == "" {
		return nil, nil
	}

	key, err := parseWeixinDirectMediaAESKey(ref.AesKey)
	if err != nil {
		return nil, err
	}
	data, err := c.downloadAndDecryptWeixinDirectCDNBuffer(ctx, ref.EncryptQueryParam, key)
	if err != nil {
		return nil, err
	}

	mimeType := http.DetectContentType(data)
	name := ensureWeixinDirectFilename(fallbackName, mimeType)
	return []bus.Media{{
		Type:     mediaType,
		Name:     name,
		Base64:   base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
		URL:      "",
	}}, nil
}

func (c *WeixinChannel) sendDirect(msg *bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("weixin channel is not running")
	}
	if err := c.ensureWeixinDirectSessionActive(); err != nil {
		return err
	}

	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		return fmt.Errorf("weixin direct send requires chat_id")
	}

	contextToken := c.resolveWeixinDirectContextToken(chatID, msg.Metadata)
	if contextToken == "" {
		return fmt.Errorf("weixin direct send requires context token for chat %s", chatID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if text := strings.TrimSpace(msg.Content); text != "" {
		if err := c.sendWeixinDirectTextMessage(ctx, chatID, contextToken, text); err != nil {
			return c.wrapWeixinDirectSendError(chatID, err)
		}
	}

	for _, media := range msg.Media {
		if err := c.sendWeixinDirectMedia(ctx, chatID, contextToken, media); err != nil {
			return c.wrapWeixinDirectSendError(chatID, err)
		}
	}

	logger.Info("Weixin direct message sent",
		zap.String("account_id", c.AccountID()),
		zap.String("chat_id", chatID),
		zap.Int("content_length", len(msg.Content)),
		zap.Int("media_count", len(msg.Media)),
	)
	return nil
}

func (c *WeixinChannel) wrapWeixinDirectSendError(chatID string, err error) error {
	logger.Error("Failed to send Weixin direct message",
		zap.String("account_id", c.AccountID()),
		zap.String("chat_id", chatID),
		zap.Error(err),
	)
	if c.remainingWeixinDirectPause() > 0 {
		return fmt.Errorf("weixin send paused: %w", err)
	}
	return err
}

func (c *WeixinChannel) sendWeixinDirectTextMessage(ctx context.Context, toUserID, contextToken, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return c.sendWeixinDirectMessageItem(ctx, toUserID, contextToken, weixinDirectMessageItem{
		Type: weixinDirectMessageItemTypeText,
		TextItem: &weixinDirectTextItem{
			Text: text,
		},
	})
}

func (c *WeixinChannel) sendWeixinDirectMedia(ctx context.Context, toUserID, contextToken string, media bus.Media) error {
	normalized := shared.NormalizeMediaType(media.Type)
	filename := shared.InferMediaFileName(media, "attachment")
	data, err := shared.MaterializeMediaData(c.client, media, weixinDirectMediaMaxBytes)
	if err != nil {
		return err
	}

	uploadType := weixinDirectUploadMediaFile
	itemType := weixinDirectMessageItemTypeFile
	switch normalized {
	case shared.UnifiedMediaImage:
		uploadType = weixinDirectUploadMediaImage
		itemType = weixinDirectMessageItemTypeImage
	case shared.UnifiedMediaVideo:
		uploadType = weixinDirectUploadMediaVideo
		itemType = weixinDirectMessageItemTypeVideo
	case shared.UnifiedMediaAudio:
		uploadType = weixinDirectUploadMediaFile
		itemType = weixinDirectMessageItemTypeFile
	default:
		uploadType = weixinDirectUploadMediaFile
		itemType = weixinDirectMessageItemTypeFile
	}

	uploaded, err := c.uploadWeixinDirectBytes(ctx, data, filename, toUserID, uploadType)
	if err != nil {
		return err
	}

	mediaRef := &weixinDirectCDNMedia{
		EncryptQueryParam: uploaded.downloadParam,
		AesKey:            base64.StdEncoding.EncodeToString([]byte(uploaded.aesKeyHex)),
		EncryptType:       1,
	}

	switch itemType {
	case weixinDirectMessageItemTypeImage:
		return c.sendWeixinDirectMessageItem(ctx, toUserID, contextToken, weixinDirectMessageItem{
			Type: weixinDirectMessageItemTypeImage,
			ImageItem: &weixinDirectImageItem{
				Media: mediaRef,
			},
		})
	case weixinDirectMessageItemTypeVideo:
		return c.sendWeixinDirectMessageItem(ctx, toUserID, contextToken, weixinDirectMessageItem{
			Type: weixinDirectMessageItemTypeVideo,
			VideoItem: &weixinDirectVideoItem{
				Media: mediaRef,
			},
		})
	default:
		return c.sendWeixinDirectMessageItem(ctx, toUserID, contextToken, weixinDirectMessageItem{
			Type: weixinDirectMessageItemTypeFile,
			FileItem: &weixinDirectFileItem{
				Media:    mediaRef,
				FileName: uploaded.filename,
			},
		})
	}
}

func (c *WeixinChannel) sendWeixinDirectMessageItem(ctx context.Context, toUserID, contextToken string, item weixinDirectMessageItem) error {
	resp, err := c.directAPI.SendMessage(ctx, weixinDirectSendMessageReq{
		Msg: weixinDirectMessage{
			ToUserID:     toUserID,
			ClientID:     "sunclaw-" + uuid.NewString(),
			MessageType:  weixinDirectMessageTypeBot,
			MessageState: weixinDirectMessageStateFinish,
			ItemList:     []weixinDirectMessageItem{item},
			ContextToken: contextToken,
		},
	})
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("sendmessage returned nil response")
	}
	if resp.Ret != 0 || resp.Errcode != 0 {
		if isWeixinDirectSessionExpiredStatus(resp.Ret, resp.Errcode) {
			c.pauseWeixinDirectSession("sendmessage", resp.Ret, resp.Errcode, resp.Errmsg)
		}
		return fmt.Errorf("sendmessage failed: ret=%d errcode=%d errmsg=%s", resp.Ret, resp.Errcode, resp.Errmsg)
	}
	return nil
}

type weixinDirectUploadedFile struct {
	downloadParam string
	aesKeyHex     string
	filename      string
}

func (c *WeixinChannel) uploadWeixinDirectBytes(ctx context.Context, data []byte, filename, toUserID string, mediaType int) (*weixinDirectUploadedFile, error) {
	if int64(len(data)) > weixinDirectMediaMaxBytes {
		return nil, fmt.Errorf("media too large: %d bytes", len(data))
	}

	filekey, err := randomWeixinDirectHex(16)
	if err != nil {
		return nil, err
	}
	aesKey := make([]byte, 16)
	if _, err := rand.Read(aesKey); err != nil {
		return nil, err
	}
	aesKeyHex := hex.EncodeToString(aesKey)
	rawMD5 := md5.Sum(data)

	resp, err := c.directAPI.GetUploadURL(ctx, weixinDirectGetUploadURLReq{
		Filekey:     filekey,
		MediaType:   mediaType,
		ToUserID:    toUserID,
		Rawsize:     int64(len(data)),
		RawfileMD5:  hex.EncodeToString(rawMD5[:]),
		Filesize:    aesECBPadSize(int64(len(data))),
		NoNeedThumb: true,
		Aeskey:      aesKeyHex,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("getuploadurl returned nil response")
	}
	if resp.Ret != 0 || resp.Errcode != 0 {
		if isWeixinDirectSessionExpiredStatus(resp.Ret, resp.Errcode) {
			c.pauseWeixinDirectSession("getuploadurl", resp.Ret, resp.Errcode, resp.Errmsg)
		}
		return nil, fmt.Errorf("getuploadurl failed: ret=%d errcode=%d errmsg=%s", resp.Ret, resp.Errcode, resp.Errmsg)
	}
	if strings.TrimSpace(resp.UploadParam) == "" {
		return nil, fmt.Errorf("getuploadurl returned empty upload_param")
	}

	downloadParam, err := c.uploadWeixinDirectBufferToCDN(ctx, data, resp.UploadParam, filekey, aesKey)
	if err != nil {
		return nil, err
	}

	return &weixinDirectUploadedFile{
		downloadParam: downloadParam,
		aesKeyHex:     aesKeyHex,
		filename:      filename,
	}, nil
}

func (c *WeixinChannel) uploadWeixinDirectBufferToCDN(ctx context.Context, plaintext []byte, uploadParam, filekey string, aesKey []byte) (string, error) {
	ciphertext, err := encryptWeixinDirectAESECB(plaintext, aesKey)
	if err != nil {
		return "", err
	}

	uploadURL := buildWeixinDirectCDNUploadURL(c.directCDNBaseURL(), uploadParam, filekey)
	var lastErr error

	for attempt := 1; attempt <= weixinDirectUploadRetryMax; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(ciphertext))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			func() {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					lastErr = fmt.Errorf("cdn upload failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
					return
				}
				encrypted := strings.TrimSpace(resp.Header.Get("X-Encrypted-Param"))
				if encrypted == "" {
					lastErr = fmt.Errorf("cdn upload missing x-encrypted-param header")
					return
				}
				uploadParam = encrypted
				lastErr = nil
			}()
		}

		if lastErr == nil {
			return uploadParam, nil
		}
		if attempt == weixinDirectUploadRetryMax || strings.Contains(lastErr.Error(), "status 4") {
			break
		}
	}

	return "", lastErr
}

func (c *WeixinChannel) resolveWeixinDirectContextToken(chatID string, metadata map[string]interface{}) string {
	if token, ok := c.contextTokens.Load(chatID); ok {
		if s, ok := token.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if metadata == nil {
		return ""
	}
	if raw, ok := metadata["context_token"]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func buildWeixinDirectSyncCursorPath(baseURL, token string) string {
	//home, err := os.UserHomeDir()
	//if err != nil {
	//	return ""
	//}
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	// 获取 workspace 目录
	workspace, err := config.GetWorkspacePath(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get workspace path: %v\n", err)
		os.Exit(1)
	}
	key := "default"
	if strings.TrimSpace(token) != "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(baseURL) + "|" + strings.TrimSpace(token)))
		key = hex.EncodeToString(sum[:8])
	}
	return filepath.Join(workspace, ".sunclaw", "channels", "weixin", "sync", key+".json")
}

func loadWeixinDirectCursor(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	var payload struct {
		GetUpdatesBuf string `json:"get_updates_buf"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	return payload.GetUpdatesBuf, nil
}

func saveWeixinDirectCursor(path, cursor string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		GetUpdatesBuf string `json:"get_updates_buf"`
	}{GetUpdatesBuf: cursor})
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (c *WeixinChannel) directCDNBaseURL() string {
	if strings.TrimSpace(c.cdnBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(c.cdnBaseURL), "/")
	}
	return weixinDirectDefaultCDNBaseURL
}

func isWeixinDirectSessionExpiredStatus(ret, errcode int) bool {
	return ret == weixinDirectSessionExpiredCode || errcode == weixinDirectSessionExpiredCode
}

func (c *WeixinChannel) pauseWeixinDirectSession(operation string, ret, errcode int, errmsg string) time.Duration {
	c.pauseMu.Lock()
	defer c.pauseMu.Unlock()

	until := time.Now().Add(weixinDirectSessionPauseDuration)
	if until.After(c.pauseUntil) {
		c.pauseUntil = until
	}

	remaining := time.Until(c.pauseUntil)
	logger.Error("Weixin session expired; pausing channel",
		zap.String("account_id", c.AccountID()),
		zap.String("operation", operation),
		zap.Int("ret", ret),
		zap.Int("errcode", errcode),
		zap.String("errmsg", errmsg),
		zap.Time("until", c.pauseUntil),
	)
	return remaining
}

func (c *WeixinChannel) remainingWeixinDirectPause() time.Duration {
	c.pauseMu.Lock()
	defer c.pauseMu.Unlock()

	if c.pauseUntil.IsZero() {
		return 0
	}
	remaining := time.Until(c.pauseUntil)
	if remaining <= 0 {
		c.pauseUntil = time.Time{}
		return 0
	}
	return remaining
}

func (c *WeixinChannel) waitWhileWeixinDirectSessionPaused(ctx context.Context) error {
	remaining := c.remainingWeixinDirectPause()
	if remaining <= 0 {
		return nil
	}
	return waitWithContext(ctx, remaining)
}

func (c *WeixinChannel) ensureWeixinDirectSessionActive() error {
	remaining := c.remainingWeixinDirectPause()
	if remaining <= 0 {
		return nil
	}
	return fmt.Errorf("weixin session paused (%d min remaining)", int((remaining+time.Minute-1)/time.Minute))
}

func buildWeixinDirectCDNDownloadURL(base, encryptedQueryParam string) string {
	return strings.TrimRight(base, "/") + "/download?encrypted_query_param=" + neturl.QueryEscape(encryptedQueryParam)
}

func buildWeixinDirectCDNUploadURL(base, uploadParam, filekey string) string {
	return strings.TrimRight(base, "/") + "/upload?encrypted_query_param=" + neturl.QueryEscape(uploadParam) + "&filekey=" + neturl.QueryEscape(filekey)
}

func (c *WeixinChannel) downloadAndDecryptWeixinDirectCDNBuffer(ctx context.Context, encryptedQueryParam string, key []byte) ([]byte, error) {
	data, err := c.downloadWeixinDirectCDNBuffer(ctx, encryptedQueryParam)
	if err != nil {
		return nil, err
	}
	return decryptWeixinDirectAESECB(data, key)
}

func (c *WeixinChannel) downloadWeixinDirectCDNBuffer(ctx context.Context, encryptedQueryParam string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildWeixinDirectCDNDownloadURL(c.directCDNBaseURL(), encryptedQueryParam), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("cdn download failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

func parseWeixinDirectMediaAESKey(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	if len(raw) == 16 {
		return raw, nil
	}
	if len(raw) == 32 {
		if decoded, err := hex.DecodeString(string(raw)); err == nil && len(decoded) == 16 {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("unexpected weixin aes key length: %d", len(raw))
}

func encryptWeixinDirectAESECB(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext = pkcs7Pad(plaintext, block.BlockSize())
	out := make([]byte, len(plaintext))
	for start := 0; start < len(plaintext); start += block.BlockSize() {
		block.Encrypt(out[start:start+block.BlockSize()], plaintext[start:start+block.BlockSize()])
	}
	return out, nil
}

func decryptWeixinDirectAESECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("invalid ciphertext length")
	}
	out := make([]byte, len(ciphertext))
	for start := 0; start < len(ciphertext); start += block.BlockSize() {
		block.Decrypt(out[start:start+block.BlockSize()], ciphertext[start:start+block.BlockSize()])
	}
	return pkcs7Unpad(out, block.BlockSize())
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	if padding == 0 {
		padding = blockSize
	}
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded payload")
	}
	padding := int(data[len(data)-1])
	if padding <= 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for i := len(data) - padding; i < len(data); i++ {
		if int(data[i]) != padding {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}

func aesECBPadSize(size int64) int64 {
	remainder := size % aes.BlockSize
	if remainder == 0 {
		return size + aes.BlockSize
	}
	return size + aes.BlockSize - remainder
}

func ensureWeixinDirectFilename(name, mimeType string) string {
	filename := strings.TrimSpace(name)
	if filename == "" {
		filename = "attachment"
	}
	if filepath.Ext(filename) != "" {
		return filename
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return filename + exts[0]
	}
	return filename
}

func randomWeixinDirectHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	return waitWithContext(ctx, d) == nil
}
