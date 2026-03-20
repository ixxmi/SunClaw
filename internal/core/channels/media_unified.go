package channels

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/smallnest/goclaw/internal/core/bus"
)

const (
	UnifiedMediaImage = "image"
	UnifiedMediaFile  = "file"
	UnifiedMediaVideo = "video"
	UnifiedMediaAudio = "audio"
)

// NormalizeMediaType normalizes media type from different channel/provider values.
func NormalizeMediaType(t string) string {
	s := strings.ToLower(strings.TrimSpace(t))
	switch s {
	case "image", "photo", "img", "picture":
		return UnifiedMediaImage
	case "file", "document", "doc", "attachment":
		return UnifiedMediaFile
	case "video":
		return UnifiedMediaVideo
	case "audio", "voice":
		return UnifiedMediaAudio
	default:
		return s
	}
}

// SelectFirstSupportedMedia returns first media item supported by channel.
func SelectFirstSupportedMedia(list []bus.Media, supported map[string]bool) (bus.Media, bool) {
	for _, m := range list {
		t := NormalizeMediaType(m.Type)
		if supported[t] {
			m.Type = t
			return m, true
		}
	}
	return bus.Media{}, false
}

// DecodeBase64Media decodes raw base64 or data-uri base64 media content.
func DecodeBase64Media(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("empty base64 payload")
	}
	if idx := strings.Index(s, ","); strings.HasPrefix(s, "data:") && idx > 0 {
		s = s[idx+1:]
	}

	b, err := base64.StdEncoding.DecodeString(s)
	if err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// InferMediaFileName infers a file name from media metadata/url.
func InferMediaFileName(m bus.Media, fallback string) string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	if u := strings.TrimSpace(m.URL); u != "" {
		if parsed, err := url.Parse(u); err == nil {
			base := path.Base(parsed.Path)
			if base != "" && base != "." && base != "/" {
				return base
			}
		}
	}
	if fallback == "" {
		fallback = "attachment"
	}
	return fallback
}

// MaterializeMediaData loads media bytes from base64/url.
func MaterializeMediaData(client *http.Client, media bus.Media, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(media.Base64) != "" {
		return DecodeBase64Media(media.Base64)
	}
	if strings.TrimSpace(media.URL) == "" {
		return nil, fmt.Errorf("media has neither base64 nor url")
	}
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Get(media.URL)
	if err != nil {
		return nil, fmt.Errorf("download media failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download media failed with status %d", resp.StatusCode)
	}

	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read media body failed: %w", err)
	}
	if maxBytes > 0 && int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("media exceeds size limit: %d > %d", len(b), maxBytes)
	}
	return b, nil
}

// AppendMediaURLsToContent appends media URL list to text for channels without native file send.
func AppendMediaURLsToContent(content string, list []bus.Media, supported map[string]bool) string {
	base := strings.TrimSpace(content)
	var lines []string
	for _, m := range list {
		t := NormalizeMediaType(m.Type)
		if len(supported) > 0 && !supported[t] {
			continue
		}
		if u := strings.TrimSpace(m.URL); u != "" {
			if t == "" {
				lines = append(lines, u)
			} else {
				lines = append(lines, fmt.Sprintf("[%s] %s", t, u))
			}
		}
	}
	if len(lines) == 0 {
		return base
	}
	if base == "" {
		return strings.Join(lines, "\n")
	}
	return base + "\n\n" + strings.Join(lines, "\n")
}
