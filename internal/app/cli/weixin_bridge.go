package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/smallnest/goclaw/internal/platform/weixinbridge"
	"github.com/spf13/cobra"
)

var (
	weixinBridgeAddr          string
	weixinBridgePublicBaseURL string
)

var weixinBridgeCmd = &cobra.Command{
	Use:   "weixin-bridge",
	Short: "Run a reference Weixin bridge",
}

var weixinBridgeServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the mock/reference Weixin bridge server",
	RunE:  runWeixinBridgeServe,
}

func init() {
	weixinBridgeServeCmd.Flags().StringVar(&weixinBridgeAddr, "addr", "127.0.0.1:19090", "Listen address for the bridge server")
	weixinBridgeServeCmd.Flags().StringVar(&weixinBridgePublicBaseURL, "public-base-url", "", "Optional public base URL used to populate qr_code_url")

	weixinBridgeCmd.AddCommand(weixinBridgeServeCmd)
	rootCmd.AddCommand(weixinBridgeCmd)
}

func runWeixinBridgeServe(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := weixinbridge.NewServer(weixinbridge.Config{
		Addr:          weixinBridgeAddr,
		PublicBaseURL: weixinBridgePublicBaseURL,
	})

	fmt.Printf("Weixin bridge mock listening on http://%s\n", weixinBridgeAddr)
	fmt.Println("Useful endpoints:")
	fmt.Printf("  GET  http://%s/health\n", weixinBridgeAddr)
	fmt.Printf("  GET  http://%s/session/status\n", weixinBridgeAddr)
	fmt.Printf("  POST http://%s/session/start\n", weixinBridgeAddr)
	fmt.Printf("  GET  http://%s/session/qrcode?session_id=<id>\n", weixinBridgeAddr)
	fmt.Printf("  POST http://%s/session/scan\n", weixinBridgeAddr)
	fmt.Printf("  POST http://%s/messages/inject\n", weixinBridgeAddr)
	fmt.Printf("  GET  http://%s/messages\n", weixinBridgeAddr)
	fmt.Printf("  POST http://%s/send\n", weixinBridgeAddr)

	return server.Serve(ctx)
}
