package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/smallnest/goclaw/internal/core/channels"
	"github.com/spf13/cobra"
)

var (
	weixinLoginBaseURL string
	weixinLoginBotType string
	weixinLoginProxy   string
	weixinLoginTimeout time.Duration
)

var weixinCmd = &cobra.Command{
	Use:   "weixin",
	Short: "Manage Weixin direct mode",
}

var weixinLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Run Weixin iLink QR login flow",
	RunE:  runWeixinLogin,
}

func init() {
	weixinLoginCmd.Flags().StringVar(&weixinLoginBaseURL, "base-url", "", "Weixin iLink base URL")
	weixinLoginCmd.Flags().StringVar(&weixinLoginBotType, "bot-type", "3", "Weixin iLink bot type")
	weixinLoginCmd.Flags().StringVar(&weixinLoginProxy, "proxy", "", "Optional HTTP proxy URL")
	weixinLoginCmd.Flags().DurationVar(&weixinLoginTimeout, "timeout", 5*time.Minute, "Login timeout")

	weixinCmd.AddCommand(weixinLoginCmd)
	rootCmd.AddCommand(weixinCmd)
}

func runWeixinLogin(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("Requesting Weixin QR code...")
	result, err := channels.PerformWeixinDirectLogin(ctx, channels.WeixinDirectLoginOptions{
		BaseURL: weixinLoginBaseURL,
		BotType: weixinLoginBotType,
		Proxy:   weixinLoginProxy,
		Timeout: weixinLoginTimeout,
		OnQRCode: func(qrcode, content string) {
			fmt.Println()
			fmt.Println("Scan this QR payload with WeChat:")
			fmt.Printf("  qrcode: %s\n", qrcode)
			fmt.Printf("  content: %s\n", content)
			fmt.Println()
			fmt.Println("Waiting for confirmation...")
		},
	})
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Weixin login successful.")
	fmt.Printf("Account ID: %s\n", result.AccountID)
	fmt.Printf("User ID: %s\n", result.UserID)
	fmt.Printf("Base URL: %s\n", result.BaseURL)
	fmt.Println()
	fmt.Println("Add this to your config:")
	fmt.Println()
	fmt.Println("channels:")
	fmt.Println("  weixin:")
	fmt.Println("    enabled: true")
	fmt.Println("    mode: direct")
	fmt.Printf("    token: %q\n", result.BotToken)
	fmt.Printf("    base_url: %q\n", result.BaseURL)
	if weixinLoginProxy != "" {
		fmt.Printf("    proxy: %q\n", weixinLoginProxy)
	}
	fmt.Println()
	return nil
}
