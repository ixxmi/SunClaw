package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"strings"
)

const (
	weixinDirectDefaultBaseURL = "https://ilinkai.weixin.qq.com/"

	weixinDirectUploadMediaImage = 1
	weixinDirectUploadMediaVideo = 2
	weixinDirectUploadMediaFile  = 3
	weixinDirectUploadMediaVoice = 4

	weixinDirectMessageTypeBot = 2

	weixinDirectMessageItemTypeText  = 1
	weixinDirectMessageItemTypeImage = 2
	weixinDirectMessageItemTypeVoice = 3
	weixinDirectMessageItemTypeFile  = 4
	weixinDirectMessageItemTypeVideo = 5

	weixinDirectMessageStateFinish = 2
)

type weixinDirectAPIClient struct {
	BaseURL    string
	Token      string
	HttpClient *http.Client
}

type weixinDirectBaseInfo struct {
	ChannelVersion string `json:"channel_version,omitempty"`
}

type weixinDirectAPIStatus struct {
	Ret     int    `json:"ret,omitempty"`
	Errcode int    `json:"errcode,omitempty"`
	Errmsg  string `json:"errmsg,omitempty"`
}

type weixinDirectCDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AesKey            string `json:"aes_key,omitempty"`
	EncryptType       int    `json:"encrypt_type,omitempty"`
}

type weixinDirectTextItem struct {
	Text string `json:"text,omitempty"`
}

type weixinDirectImageItem struct {
	Media      *weixinDirectCDNMedia `json:"media,omitempty"`
	ThumbMedia *weixinDirectCDNMedia `json:"thumb_media,omitempty"`
}

type weixinDirectVoiceItem struct {
	Media *weixinDirectCDNMedia `json:"media,omitempty"`
	Text  string                `json:"text,omitempty"`
}

type weixinDirectFileItem struct {
	Media    *weixinDirectCDNMedia `json:"media,omitempty"`
	FileName string                `json:"file_name,omitempty"`
}

type weixinDirectVideoItem struct {
	Media *weixinDirectCDNMedia `json:"media,omitempty"`
}

type weixinDirectRefMessage struct {
	MessageItem *weixinDirectMessageItem `json:"message_item,omitempty"`
}

type weixinDirectMessageItem struct {
	Type      int                     `json:"type,omitempty"`
	RefMsg    *weixinDirectRefMessage `json:"ref_msg,omitempty"`
	TextItem  *weixinDirectTextItem   `json:"text_item,omitempty"`
	ImageItem *weixinDirectImageItem  `json:"image_item,omitempty"`
	VoiceItem *weixinDirectVoiceItem  `json:"voice_item,omitempty"`
	FileItem  *weixinDirectFileItem   `json:"file_item,omitempty"`
	VideoItem *weixinDirectVideoItem  `json:"video_item,omitempty"`
}

type weixinDirectMessage struct {
	MessageID    int64                     `json:"message_id,omitempty"`
	FromUserID   string                    `json:"from_user_id,omitempty"`
	ToUserID     string                    `json:"to_user_id,omitempty"`
	ClientID     string                    `json:"client_id,omitempty"`
	SessionID    string                    `json:"session_id,omitempty"`
	MessageType  int                       `json:"message_type,omitempty"`
	MessageState int                       `json:"message_state,omitempty"`
	ItemList     []weixinDirectMessageItem `json:"item_list,omitempty"`
	ContextToken string                    `json:"context_token,omitempty"`
}

type weixinDirectGetUpdatesReq struct {
	GetUpdatesBuf string               `json:"get_updates_buf,omitempty"`
	BaseInfo      weixinDirectBaseInfo `json:"base_info,omitempty"`
}

type weixinDirectGetUpdatesResp struct {
	weixinDirectAPIStatus
	Msgs                 []weixinDirectMessage `json:"msgs,omitempty"`
	GetUpdatesBuf        string                `json:"get_updates_buf,omitempty"`
	LongpollingTimeoutMs int                   `json:"longpolling_timeout_ms,omitempty"`
}

type weixinDirectSendMessageReq struct {
	Msg      weixinDirectMessage  `json:"msg,omitempty"`
	BaseInfo weixinDirectBaseInfo `json:"base_info,omitempty"`
}

type weixinDirectSendMessageResp struct {
	weixinDirectAPIStatus
}

type weixinDirectGetUploadURLReq struct {
	Filekey     string               `json:"filekey,omitempty"`
	MediaType   int                  `json:"media_type,omitempty"`
	ToUserID    string               `json:"to_user_id,omitempty"`
	Rawsize     int64                `json:"rawsize,omitempty"`
	RawfileMD5  string               `json:"rawfilemd5,omitempty"`
	Filesize    int64                `json:"filesize,omitempty"`
	NoNeedThumb bool                 `json:"no_need_thumb,omitempty"`
	Aeskey      string               `json:"aeskey,omitempty"`
	BaseInfo    weixinDirectBaseInfo `json:"base_info,omitempty"`
}

type weixinDirectGetUploadURLResp struct {
	weixinDirectAPIStatus
	UploadParam string `json:"upload_param,omitempty"`
}

type weixinDirectQRCodeResponse struct {
	Qrcode           string `json:"qrcode"`
	QrcodeImgContent string `json:"qrcode_img_content"`
}

type weixinDirectQRCodeStatusResponse struct {
	Status      string `json:"status"`
	BotToken    string `json:"bot_token,omitempty"`
	IlinkBotID  string `json:"ilink_bot_id,omitempty"`
	Baseurl     string `json:"baseurl,omitempty"`
	IlinkUserID string `json:"ilink_user_id,omitempty"`
}

func newWeixinDirectAPIClient(baseURL, token string, client *http.Client) (*weixinDirectAPIClient, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = weixinDirectDefaultBaseURL
	}
	if _, err := neturl.Parse(base); err != nil {
		return nil, fmt.Errorf("invalid weixin base url: %w", err)
	}
	if client == nil {
		client = &http.Client{}
	}
	return &weixinDirectAPIClient{
		BaseURL:    base,
		Token:      strings.TrimSpace(token),
		HttpClient: client,
	}, nil
}

func (c *weixinDirectAPIClient) post(ctx context.Context, endpoint string, body any, responseObj any) error {
	u, err := neturl.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	u.Path = path.Join(u.Path, endpoint)

	jsonData, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header["AuthorizationType"] = []string{"ilink_bot_token"}
	req.Header["X-WECHAT-UIN"] = []string{randomWeixinDirectUIN()}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http POST %s failed: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(respBody)))
	}

	if responseObj != nil {
		if err := json.Unmarshal(respBody, responseObj); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}
	return nil
}

func (c *weixinDirectAPIClient) GetUpdates(ctx context.Context, req weixinDirectGetUpdatesReq) (*weixinDirectGetUpdatesResp, error) {
	req.BaseInfo = weixinDirectBaseInfo{ChannelVersion: "1.0.2"}
	var resp weixinDirectGetUpdatesResp
	if err := c.post(ctx, "ilink/bot/getupdates", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *weixinDirectAPIClient) SendMessage(ctx context.Context, req weixinDirectSendMessageReq) (*weixinDirectSendMessageResp, error) {
	req.BaseInfo = weixinDirectBaseInfo{ChannelVersion: "1.0.2"}
	var resp weixinDirectSendMessageResp
	if err := c.post(ctx, "ilink/bot/sendmessage", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *weixinDirectAPIClient) GetUploadURL(ctx context.Context, req weixinDirectGetUploadURLReq) (*weixinDirectGetUploadURLResp, error) {
	req.BaseInfo = weixinDirectBaseInfo{ChannelVersion: "1.0.2"}
	var resp weixinDirectGetUploadURLResp
	if err := c.post(ctx, "ilink/bot/getuploadurl", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *weixinDirectAPIClient) GetQRCode(ctx context.Context, botType string) (*weixinDirectQRCodeResponse, error) {
	u, err := neturl.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "ilink/bot/get_bot_qrcode")
	q := u.Query()
	q.Set("bot_type", botType)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get_bot_qrcode failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded weixinDirectQRCodeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func (c *weixinDirectAPIClient) GetQRCodeStatus(ctx context.Context, qrcode string) (*weixinDirectQRCodeStatusResponse, error) {
	u, err := neturl.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "ilink/bot/get_qrcode_status")
	q := u.Query()
	q.Set("qrcode", qrcode)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header["iLink-App-ClientVersion"] = []string{"1"}

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get_qrcode_status failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded weixinDirectQRCodeStatusResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func randomWeixinDirectUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	uint32Val := binary.BigEndian.Uint32(b[:])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", uint32Val)))
}
