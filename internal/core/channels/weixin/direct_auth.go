package weixin

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type WeixinDirectLoginOptions struct {
	BaseURL      string
	BotType      string
	Proxy        string
	Timeout      time.Duration
	PollInterval time.Duration
	OnQRCode     func(qrcode, content string)
}

type WeixinDirectLoginResult struct {
	BotToken    string
	UserID      string
	AccountID   string
	BaseURL     string
	QRCode      string
	QRCodeValue string
}

func PerformWeixinDirectLogin(ctx context.Context, opts WeixinDirectLoginOptions) (*WeixinDirectLoginResult, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = weixinDirectDefaultBaseURL
	}
	if strings.TrimSpace(opts.BotType) == "" {
		opts.BotType = "3"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}

	client, err := newWeixinHTTPClient(opts.Proxy)
	if err != nil {
		return nil, err
	}
	api, err := newWeixinDirectAPIClient(opts.BaseURL, "", client)
	if err != nil {
		return nil, err
	}

	qrResp, err := api.GetQRCode(ctx, opts.BotType)
	if err != nil {
		return nil, fmt.Errorf("failed to get qrcode: %w", err)
	}
	if opts.OnQRCode != nil {
		opts.OnQRCode(qrResp.Qrcode, qrResp.QrcodeImgContent)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return nil, fmt.Errorf("weixin login timeout")
		case <-ticker.C:
			statusResp, err := api.GetQRCodeStatus(timeoutCtx, qrResp.Qrcode)
			if err != nil {
				continue
			}
			switch statusResp.Status {
			case "wait", "scaned":
				continue
			case "confirmed":
				if strings.TrimSpace(statusResp.BotToken) == "" || strings.TrimSpace(statusResp.IlinkBotID) == "" {
					return nil, fmt.Errorf("login confirmed but missing bot_token or ilink_bot_id")
				}
				baseURL := strings.TrimSpace(statusResp.Baseurl)
				if baseURL == "" {
					baseURL = strings.TrimSpace(opts.BaseURL)
				}
				return &WeixinDirectLoginResult{
					BotToken:    statusResp.BotToken,
					UserID:      statusResp.IlinkUserID,
					AccountID:   statusResp.IlinkBotID,
					BaseURL:     baseURL,
					QRCode:      qrResp.Qrcode,
					QRCodeValue: qrResp.QrcodeImgContent,
				}, nil
			case "expired":
				return nil, fmt.Errorf("weixin qrcode expired, please try again")
			default:
				continue
			}
		}
	}
}
