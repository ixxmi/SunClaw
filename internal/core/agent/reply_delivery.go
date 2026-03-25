package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels"
	"github.com/smallnest/goclaw/internal/core/config"
)

type replyDeliveryExecutor struct {
	cfg        *config.Config
	bus        *bus.MessageBus
	channelMgr *channels.Manager
	sleep      func(context.Context, time.Duration) error
}

func newReplyDeliveryExecutor(cfg *config.Config, messageBus *bus.MessageBus, channelMgr *channels.Manager) *replyDeliveryExecutor {
	return &replyDeliveryExecutor{
		cfg:        cfg,
		bus:        messageBus,
		channelMgr: channelMgr,
		sleep:      sleepWithContext,
	}
}

func (e *replyDeliveryExecutor) Publish(ctx context.Context, outbound *bus.OutboundMessage) error {
	if outbound == nil {
		return fmt.Errorf("outbound message is nil")
	}
	if e.bus == nil {
		return fmt.Errorf("message bus is nil")
	}

	deliveryCfg := e.deliveryConfig(outbound.Channel, outbound.AccountID)
	mode := e.deliveryMode(deliveryCfg, outbound)

	switch mode {
	case config.ReplyDeliveryModeMultiPush:
		return e.publishMultiPush(ctx, outbound, deliveryCfg)
	case config.ReplyDeliveryModeStreamEdit:
		if err := e.publishStreamEdit(ctx, outbound, deliveryCfg); err != nil {
			return e.publishMultiPush(ctx, outbound, deliveryCfg)
		}
		return nil
	default:
		return e.bus.PublishOutbound(ctx, outbound)
	}
}

func (e *replyDeliveryExecutor) deliveryMode(deliveryCfg config.ReplyDeliveryConfig, outbound *bus.OutboundMessage) string {
	mode := config.NormalizeReplyDeliveryMode(deliveryCfg.Mode)
	if mode == "" {
		mode = config.ReplyDeliveryModeSingle
	}

	switch mode {
	case config.ReplyDeliveryModeHybrid:
		if e.channelMgr != nil && e.channelMgr.SupportsStreamEdit(outbound) {
			return config.ReplyDeliveryModeStreamEdit
		}
		return config.ReplyDeliveryModeMultiPush
	case config.ReplyDeliveryModeStreamEdit:
		if e.channelMgr == nil || !e.channelMgr.SupportsStreamEdit(outbound) {
			return config.ReplyDeliveryModeMultiPush
		}
		return config.ReplyDeliveryModeStreamEdit
	default:
		return mode
	}
}

func (e *replyDeliveryExecutor) deliveryConfig(channel, accountID string) config.ReplyDeliveryConfig {
	cfg := config.DefaultReplyDeliveryConfig()
	if e.cfg == nil {
		return cfg
	}
	return config.MergeReplyDeliveryConfig(cfg, e.cfg.ResolveReplyDelivery(channel, accountID))
}

func (e *replyDeliveryExecutor) publishMultiPush(ctx context.Context, outbound *bus.OutboundMessage, cfg config.ReplyDeliveryConfig) error {
	chunks := splitReplyForDelivery(outbound.Content, cfg)
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) == 1 {
		single := *outbound
		single.Content = chunks[0]
		return e.bus.PublishOutbound(ctx, &single)
	}

	for i, chunk := range chunks {
		msg := *outbound
		msg.Content = chunk
		if i > 0 {
			msg.ReplyTo = ""
		}

		if err := e.bus.PublishOutbound(ctx, &msg); err != nil {
			return err
		}

		if i < len(chunks)-1 {
			if err := e.sleep(ctx, deliveryPauseDuration(cfg)); err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *replyDeliveryExecutor) publishStreamEdit(ctx context.Context, outbound *bus.OutboundMessage, cfg config.ReplyDeliveryConfig) error {
	if e.channelMgr == nil {
		return fmt.Errorf("channel manager is nil")
	}

	chunks := splitReplyForDelivery(outbound.Content, cfg)
	if len(chunks) == 0 {
		return nil
	}

	stream := make(chan *bus.StreamMessage, len(chunks)+1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- e.channelMgr.DispatchStream(outbound, stream)
	}()

	streamMeta := buildStreamMetadata(outbound)
	for i, chunk := range chunks {
		msg := &bus.StreamMessage{
			Channel:    outbound.Channel,
			ChatID:     outbound.ChatID,
			Content:    chunk,
			ChunkIndex: i,
		}
		if i == 0 && len(streamMeta) > 0 {
			msg.Metadata = streamMeta
		}
		stream <- msg

		if i < len(chunks)-1 {
			if err := e.sleep(ctx, deliveryPauseDuration(cfg)); err != nil {
				close(stream)
				<-errCh
				return err
			}
		}
	}

	stream <- &bus.StreamMessage{
		Channel:    outbound.Channel,
		ChatID:     outbound.ChatID,
		ChunkIndex: len(chunks),
		IsComplete: true,
	}
	close(stream)

	return <-errCh
}

func splitReplyForDelivery(content string, cfg config.ReplyDeliveryConfig) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	if chunks := splitConversationalTwoBeat(trimmed); len(chunks) > 0 {
		return chunks
	}

	minChars := cfg.MinChunkChars
	if minChars <= 0 {
		minChars = config.DefaultReplyDeliveryMinChunkChars
	}
	maxChars := cfg.MaxChunkChars
	if maxChars <= 0 {
		maxChars = config.DefaultReplyDeliveryMaxChunkChars
	}
	if minChars > maxChars {
		minChars = maxChars
	}
	maxPushCount := cfg.MaxPushCount
	if maxPushCount <= 0 {
		maxPushCount = config.DefaultReplyDeliveryMaxPushCount
	}

	if utf8.RuneCountInString(trimmed) <= minChars {
		return []string{trimmed}
	}

	runes := []rune(trimmed)
	chunks := make([]string, 0, maxPushCount)
	start := 0

	for start < len(runes) {
		for start < len(runes) && unicode.IsSpace(runes[start]) {
			start++
		}
		if start >= len(runes) {
			break
		}
		if len(runes)-start <= maxChars {
			chunks = append(chunks, strings.TrimSpace(string(runes[start:])))
			break
		}

		limit := start + maxChars
		split := -1
		for i := start; i < limit; i++ {
			if i-start+1 >= minChars && isStrongReplyBoundary(runes[i]) {
				split = i + 1
			}
		}
		if split == -1 {
			for i := start; i < limit; i++ {
				if i-start+1 >= minChars && isWeakReplyBoundary(runes[i]) {
					split = i + 1
					break
				}
			}
		}
		if split == -1 {
			for i := start + minChars - 1; i < limit; i++ {
				if unicode.IsSpace(runes[i]) {
					split = i + 1
					break
				}
			}
		}
		if split == -1 || split <= start {
			split = limit
		}

		chunk := strings.TrimSpace(string(runes[start:split]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = split
	}

	if len(chunks) <= maxPushCount {
		return chunks
	}

	merged := append([]string{}, chunks[:maxPushCount-1]...)
	merged = append(merged, strings.TrimSpace(strings.Join(chunks[maxPushCount-1:], "\n\n")))
	return merged
}

func splitConversationalTwoBeat(content string) []string {
	if strings.ContainsAny(content, "\n`#*[]<>|") {
		return nil
	}

	totalRunes := utf8.RuneCountInString(content)
	if totalRunes < 12 || totalRunes > 80 {
		return nil
	}

	sentences := splitReplySentences(content)
	if len(sentences) < 2 || len(sentences) > 3 {
		return nil
	}

	first := strings.TrimSpace(sentences[0])
	second := strings.TrimSpace(strings.Join(sentences[1:], ""))
	if first == "" || second == "" {
		return nil
	}

	firstLen := utf8.RuneCountInString(first)
	secondLen := utf8.RuneCountInString(second)
	if firstLen < 4 || firstLen > 36 || secondLen < 6 || secondLen > 52 {
		return nil
	}

	if !looksEmpatheticFirstBeat(first) || !looksInvitationalSecondBeat(second) {
		return nil
	}

	return []string{first, second}
}

func splitReplySentences(content string) []string {
	runes := []rune(content)
	start := 0
	var sentences []string

	for i, r := range runes {
		if !isStrongReplyBoundary(r) || r == '\n' {
			continue
		}
		part := strings.TrimSpace(string(runes[start : i+1]))
		if part != "" {
			sentences = append(sentences, part)
		}
		start = i + 1
	}

	if start < len(runes) {
		part := strings.TrimSpace(string(runes[start:]))
		if part != "" {
			sentences = append(sentences, part)
		}
	}

	return sentences
}

func looksEmpatheticFirstBeat(text string) bool {
	lower := strings.ToLower(text)
	keywords := []string{
		"哎", "唉", "嗯", "抱抱", "难受", "难过", "心情不好", "委屈", "辛苦", "不容易", "心疼", "理解你", "能理解",
		"sorry", "that sounds hard", "that sounds really hard", "that sucks", "i'm sorry", "i am sorry", "rough",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func looksInvitationalSecondBeat(text string) bool {
	lower := strings.ToLower(text)
	keywords := []string{
		"发生什么事", "怎么了", "想说就说", "我听着", "我在", "跟我说", "和我说说", "说说看", "愿意说", "可以跟我说", "要不要聊", "我陪你",
		"what happened", "want to talk", "talk to me", "tell me", "i'm here", "i am here", "you can tell me", "talk about it",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return strings.ContainsAny(text, "？?")
}

func isStrongReplyBoundary(r rune) bool {
	switch r {
	case '\n', '.', '!', '?', '。', '！', '？':
		return true
	default:
		return false
	}
}

func isWeakReplyBoundary(r rune) bool {
	switch r {
	case ',', ';', ':', '，', '；', '：', '、':
		return true
	default:
		return false
	}
}

func buildStreamMetadata(outbound *bus.OutboundMessage) map[string]interface{} {
	if outbound == nil {
		return nil
	}

	var metadata map[string]interface{}
	if len(outbound.Metadata) > 0 {
		metadata = make(map[string]interface{}, len(outbound.Metadata)+1)
		for key, value := range outbound.Metadata {
			metadata[key] = value
		}
	}
	if outbound.ReplyTo != "" {
		if metadata == nil {
			metadata = make(map[string]interface{}, 1)
		}
		metadata["reply_to"] = outbound.ReplyTo
	}
	return metadata
}

func deliveryPauseDuration(cfg config.ReplyDeliveryConfig) time.Duration {
	minDelay := cfg.MinDelayMs
	maxDelay := cfg.MaxDelayMs
	if minDelay <= 0 && maxDelay <= 0 {
		return 0
	}
	if minDelay <= 0 {
		minDelay = maxDelay
	}
	if maxDelay <= 0 {
		maxDelay = minDelay
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	return time.Duration((minDelay+maxDelay)/2) * time.Millisecond
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
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
