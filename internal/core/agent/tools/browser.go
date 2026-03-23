package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/protocol/dom"
	"github.com/mafredri/cdp/protocol/emulation"
	"github.com/mafredri/cdp/protocol/input"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/protocol/runtime"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// CDPExecutor CDP 命令执行器接口（支持直接 CDP 和 Relay 两种模式）
type CDPExecutor interface {
	ExecuteCDP(ctx context.Context, method string, params map[string]interface{}) (map[string]interface{}, error)
	IsDirectMode() bool
	GetDirectClient() (*cdp.Client, error)
}

// BrowserCDPExecutor CDP 执行器实现
type BrowserCDPExecutor struct {
	session *BrowserSessionManager
}

// ExecuteCDP 执行 CDP 命令
func (e *BrowserCDPExecutor) ExecuteCDP(ctx context.Context, method string, params map[string]interface{}) (map[string]interface{}, error) {
	// 如果是 Relay 模式，使用 Relay 客户端
	if e.session.IsRelayMode() {
		relayClient := e.session.GetRelayClient()
		if relayClient == nil || !relayClient.IsReady() {
			return nil, fmt.Errorf("relay session not ready")
		}
		return relayClient.Execute(ctx, method, params)
	}

	// 直接模式需要通过 CDP 客户端执行
	// 这里返回错误，因为直接 CDP 模式应该使用 GetDirectClient
	return nil, fmt.Errorf("direct CDP mode should use GetDirectClient")
}

// IsDirectMode 检查是否为直接模式
func (e *BrowserCDPExecutor) IsDirectMode() bool {
	return !e.session.IsRelayMode()
}

// GetDirectClient 获取直接 CDP 客户端
func (e *BrowserCDPExecutor) GetDirectClient() (*cdp.Client, error) {
	return e.session.GetClient()
}

// BrowserTool Browser tool using Chrome DevTools Protocol or OpenClaw Relay
type BrowserTool struct {
	headless  bool
	timeout   time.Duration
	outputDir string // 固定输出目录，截图将保存到这里
	relayURL  string // OpenClaw Relay URL
	relayMode string // Connection mode: "auto", "direct", "relay"
}

// NewBrowserTool Create browser tool
func NewBrowserTool(headless bool, timeout int) *BrowserTool {
	return NewBrowserToolWithRelay(headless, timeout, "", "auto")
}

// NewBrowserToolWithRelay Create browser tool with Relay support
func NewBrowserToolWithRelay(headless bool, timeout int, relayURL, relayMode string) *BrowserTool {
	var t time.Duration
	if timeout > 0 {
		t = time.Duration(timeout) * time.Second
	} else {
		t = 30 * time.Second
	}

	// 设置固定输出目录用于保存截图
	homeDir, _ := os.UserHomeDir()
	outputDir := filepath.Join(homeDir, "goclaw-screenshots")

	return &BrowserTool{
		headless:  headless,
		timeout:   t,
		outputDir: outputDir,
		relayURL:  relayURL,
		relayMode: relayMode,
	}
}

const (
	windowModeAuto        = "auto"
	windowModeInteractive = "interactive"
	windowModeHeadless    = "headless"
)

func browserWindowModeProperty(defaultBehavior string) map[string]interface{} {
	return map[string]interface{}{
		"type": "string",
		"description": fmt.Sprintf(
			"Browser window mode. \"interactive\" opens a visible local Chrome window for the user, \"headless\" runs invisible automation, \"auto\" uses the tool default. Default behavior: %s",
			defaultBehavior,
		),
		"enum": []string{windowModeAuto, windowModeInteractive, windowModeHeadless},
	}
}

func (b *BrowserTool) parseWindowMode(params map[string]interface{}, defaultHeadless bool) (bool, bool, error) {
	raw, ok := params["window_mode"]
	if !ok {
		return defaultHeadless, false, nil
	}

	mode, ok := raw.(string)
	if !ok {
		return false, false, fmt.Errorf("window_mode must be a string")
	}

	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", windowModeAuto:
		return defaultHeadless, false, nil
	case windowModeInteractive:
		return false, true, nil
	case windowModeHeadless:
		return true, true, nil
	default:
		return false, false, fmt.Errorf("unsupported window_mode: %s", mode)
	}
}

func (b *BrowserTool) resolveConnectionMode(headless bool, forceDirect bool) ConnectionMode {
	if forceDirect || !headless {
		return ModeDirect
	}

	switch strings.ToLower(strings.TrimSpace(b.relayMode)) {
	case "direct":
		return ModeDirect
	case "relay":
		return ModeRelay
	default:
		return ModeAuto
	}
}

func (b *BrowserTool) ensureSession(
	params map[string]interface{},
	defaultHeadless bool,
	allowCreate bool,
	preferDefaultOnReuse bool,
	forceDirect bool,
) (*BrowserSessionManager, error) {
	sessionMgr := GetBrowserSession()
	requestedHeadless, explicit, err := b.parseWindowMode(params, defaultHeadless)
	if err != nil {
		return nil, err
	}

	if !sessionMgr.IsReady() {
		if !allowCreate {
			return nil, fmt.Errorf("browser session not ready. Please navigate to a page first using browser_navigate.")
		}
		if err := sessionMgr.StartWithPreferences(
			b.timeout,
			b.relayURL,
			b.resolveConnectionMode(requestedHeadless, forceDirect),
			requestedHeadless,
		); err != nil {
			return nil, fmt.Errorf("failed to start browser session: %w", err)
		}
		return sessionMgr, nil
	}

	needsRestart := false
	switch {
	case explicit:
		needsRestart = true
	case preferDefaultOnReuse && sessionMgr.IsHeadless() != requestedHeadless:
		needsRestart = true
	case forceDirect && sessionMgr.IsRelayMode():
		needsRestart = true
	}

	if needsRestart {
		if err := sessionMgr.StartWithPreferences(
			b.timeout,
			b.relayURL,
			b.resolveConnectionMode(requestedHeadless, forceDirect),
			requestedHeadless,
		); err != nil {
			return nil, fmt.Errorf("failed to switch browser session mode: %w", err)
		}
	}

	return sessionMgr, nil
}

func (b *BrowserTool) sessionModeLabel(sessionMgr *BrowserSessionManager) string {
	if sessionMgr != nil && sessionMgr.IsHeadless() {
		return windowModeHeadless
	}
	return windowModeInteractive
}

// Close Close browser tool and cleanup resources
func (b *BrowserTool) Close() error {
	// 确保输出目录存在
	if b.outputDir != "" {
		if err := os.MkdirAll(b.outputDir, 0755); err != nil {
			logger.Warn("Failed to create output dir", zap.Error(err))
		}
	}

	return nil
}

// BrowserNavigate Navigate browser to URL
func (b *BrowserTool) BrowserNavigate(ctx context.Context, params map[string]interface{}) (string, error) {
	urlStr, ok := params["url"].(string)
	if !ok {
		return "", fmt.Errorf("url parameter is required")
	}

	if _, err := url.Parse(urlStr); err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	logger.Debug("Browser navigating to", zap.String("url", urlStr))

	sessionMgr, err := b.ensureSession(params, false, true, true, false)
	if err != nil {
		return "", err
	}

	// Relay 模式下的处理
	if sessionMgr.IsRelayMode() {
		return b.navigateViaRelay(ctx, urlStr)
	}

	// 直接 CDP 模式下的处理
	client, err := sessionMgr.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get browser client: %w", err)
	}

	navArgs := page.NewNavigateArgs(urlStr)
	nav, err := client.Page.Navigate(ctx, navArgs)
	if err != nil {
		sessionMgr.Stop()
		return "", fmt.Errorf("failed to navigate: %w", err)
	}

	domContentLoaded, err := client.Page.DOMContentEventFired(ctx)
	if err != nil {
		logger.Warn("DOMContentEventFired failed, continuing anyway", zap.Error(err))
	} else {
		defer domContentLoaded.Close()
		if _, err := domContentLoaded.Recv(); err != nil {
			logger.Warn("WaitForLoadEventFired failed, continuing anyway", zap.Error(err))
		}
	}

	doc, err := client.DOM.GetDocument(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get document: %w", err)
	}

	html, err := client.DOM.GetOuterHTML(ctx, &dom.GetOuterHTMLArgs{
		NodeID: &doc.Root.NodeID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get outer HTML: %w", err)
	}

	return fmt.Sprintf(
		"Navigated to: %s\nFrame ID: %s\nWindow mode: %s\nPage size: %d bytes",
		urlStr,
		nav.FrameID,
		b.sessionModeLabel(sessionMgr),
		len(html.OuterHTML),
	), nil
}

// navigateViaRelay 通过 Relay 执行导航
func (b *BrowserTool) navigateViaRelay(ctx context.Context, urlStr string) (string, error) {
	sessionMgr := GetBrowserSession()
	relayClient := sessionMgr.GetRelayClient()
	if relayClient == nil {
		return "", fmt.Errorf("relay client not available")
	}

	params := map[string]interface{}{
		"url": urlStr,
	}

	result, err := relayClient.Execute(ctx, "Page.navigate", params)
	if err != nil {
		return "", fmt.Errorf("failed to navigate via relay: %w", err)
	}

	frameID, _ := result["frameId"].(string)
	return fmt.Sprintf(
		"Navigated to: %s (via Relay)\nFrame ID: %s\nWindow mode: %s",
		urlStr,
		frameID,
		b.sessionModeLabel(sessionMgr),
	), nil
}

// BrowserScreenshot Take screenshot of page
func (b *BrowserTool) BrowserScreenshot(ctx context.Context, params map[string]interface{}) (string, error) {
	var urlStr string
	var width, height int

	if u, ok := params["url"].(string); ok {
		urlStr = u
	}
	if w, ok := params["width"].(float64); ok {
		width = int(w)
	} else {
		width = 1920
	}
	if h, ok := params["height"].(float64); ok {
		height = int(h)
	} else {
		height = 1080
	}

	logger.Debug("Browser screenshot", zap.String("url", urlStr), zap.Int("width", width), zap.Int("height", height))

	sessionMgr, err := b.ensureSession(params, true, urlStr != "", false, false)
	if err != nil {
		return "", err
	}

	// Relay 模式下的处理
	if sessionMgr.IsRelayMode() {
		return b.screenshotViaRelay(ctx, urlStr, width, height)
	}

	// 直接 CDP 模式下的处理
	client, err := sessionMgr.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get browser client: %w", err)
	}

	if err := client.Emulation.SetDeviceMetricsOverride(ctx, emulation.NewSetDeviceMetricsOverrideArgs(
		width, height, 1.0, false,
	)); err != nil {
		logger.Warn("Failed to set viewport size", zap.Error(err))
	}

	if urlStr != "" {
		if _, err := client.Page.Navigate(ctx, page.NewNavigateArgs(urlStr)); err != nil {
			return "", fmt.Errorf("failed to navigate: %w", err)
		}
		domContentLoaded, err := client.Page.DOMContentEventFired(ctx)
		if err != nil {
			logger.Warn("DOMContentEventFired failed", zap.Error(err))
		} else {
			defer domContentLoaded.Close()
			_, _ = domContentLoaded.Recv()
		}
	}

	frameTree, err := client.Page.GetFrameTree(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get frame tree: %w", err)
	}
	currentURL := frameTree.FrameTree.Frame.URL

	screenshotArgs := page.NewCaptureScreenshotArgs().SetFormat("png")
	screenshot, err := client.Page.CaptureScreenshot(ctx, screenshotArgs)
	if err != nil {
		return "", fmt.Errorf("failed to capture screenshot: %w", err)
	}

	filename := fmt.Sprintf("screenshot_%d.png", time.Now().Unix())
	filepath := b.outputDir + string(os.PathSeparator) + filename
	if err := os.WriteFile(filepath, screenshot.Data, 0644); err != nil {
		return "", fmt.Errorf("failed to save screenshot: %w", err)
	}

	base64Str := base64.StdEncoding.EncodeToString(screenshot.Data)

	return fmt.Sprintf(
		"Screenshot saved to: %s\nURL: %s\nWindow mode: %s\nBase64 length: %d bytes\nImage URL: file://%s",
		filepath,
		currentURL,
		b.sessionModeLabel(sessionMgr),
		len(base64Str),
		filepath,
	), nil
}

// screenshotViaRelay 通过 Relay 执行截图
func (b *BrowserTool) screenshotViaRelay(ctx context.Context, urlStr string, width, height int) (string, error) {
	sessionMgr := GetBrowserSession()
	relayClient := sessionMgr.GetRelayClient()
	if relayClient == nil {
		return "", fmt.Errorf("relay client not available")
	}

	// 设置视口大小
	viewportParams := map[string]interface{}{
		"width":             width,
		"height":            height,
		"deviceScaleFactor": 1.0,
		"mobile":            false,
	}
	_, err := relayClient.Execute(ctx, "Emulation.setDeviceMetricsOverride", viewportParams)
	if err != nil {
		logger.Warn("Failed to set viewport size via relay", zap.Error(err))
	}

	// 如果提供了 URL，先导航
	if urlStr != "" {
		navParams := map[string]interface{}{"url": urlStr}
		_, err := relayClient.Execute(ctx, "Page.navigate", navParams)
		if err != nil {
			return "", fmt.Errorf("failed to navigate via relay: %w", err)
		}
	}

	// 捕获截图
	captureParams := map[string]interface{}{
		"format": "png",
	}
	result, err := relayClient.Execute(ctx, "Page.captureScreenshot", captureParams)
	if err != nil {
		return "", fmt.Errorf("failed to capture screenshot via relay: %w", err)
	}

	data, ok := result["data"].(string)
	if !ok {
		return "", fmt.Errorf("invalid screenshot data from relay")
	}

	// 解码 base64 数据
	screenshotData, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", fmt.Errorf("failed to decode screenshot data: %w", err)
	}

	filename := fmt.Sprintf("screenshot_%d.png", time.Now().Unix())
	filepath := b.outputDir + string(os.PathSeparator) + filename
	if err := os.WriteFile(filepath, screenshotData, 0644); err != nil {
		return "", fmt.Errorf("failed to save screenshot: %w", err)
	}

	return fmt.Sprintf(
		"Screenshot saved to: %s (via Relay)\nWindow mode: %s\nBase64 length: %d bytes\nImage URL: file://%s",
		filepath,
		b.sessionModeLabel(sessionMgr),
		len(data),
		filepath,
	), nil
}

// BrowserExecuteScript Execute JavaScript in browser
func (b *BrowserTool) BrowserExecuteScript(ctx context.Context, params map[string]interface{}) (string, error) {
	script, ok := params["script"].(string)
	if !ok {
		return "", fmt.Errorf("script parameter is required")
	}

	urlStr := ""
	if u, ok := params["url"].(string); ok {
		urlStr = u
	}

	logger.Debug("Browser executing script", zap.String("url", urlStr), zap.String("script", script))

	sessionMgr, err := b.ensureSession(params, true, urlStr != "", false, true)
	if err != nil {
		return "", err
	}

	client, err := sessionMgr.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get browser client: %w", err)
	}

	if urlStr != "" {
		if _, err := client.Page.Navigate(ctx, page.NewNavigateArgs(urlStr)); err != nil {
			return "", fmt.Errorf("failed to navigate: %w", err)
		}
		domContentLoaded, err := client.Page.DOMContentEventFired(ctx)
		if err != nil {
			logger.Warn("DOMContentEventFired failed", zap.Error(err))
		} else {
			defer domContentLoaded.Close()
			_, _ = domContentLoaded.Recv()
		}
	}

	evalArgs := runtime.NewEvaluateArgs(script).SetReturnByValue(true)
	result, err := client.Runtime.Evaluate(ctx, evalArgs)
	if err != nil {
		return "", fmt.Errorf("failed to execute script: %w", err)
	}

	resultJSON, err := formatCDPResult(&result.Result)
	if err != nil {
		return "", fmt.Errorf("failed to format result: %w", err)
	}

	return resultJSON, nil
}

// BrowserClick Click element on page
func (b *BrowserTool) BrowserClick(ctx context.Context, params map[string]interface{}) (string, error) {
	urlStr := ""
	selector, ok := params["selector"].(string)
	if !ok {
		return "", fmt.Errorf("selector parameter is required")
	}

	if u, ok := params["url"].(string); ok {
		urlStr = u
	}

	logger.Debug("Browser clicking element", zap.String("url", urlStr), zap.String("selector", selector))

	sessionMgr, err := b.ensureSession(params, false, urlStr != "", false, true)
	if err != nil {
		return "", err
	}

	client, err := sessionMgr.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get browser client: %w", err)
	}

	if urlStr != "" {
		if _, err := client.Page.Navigate(ctx, page.NewNavigateArgs(urlStr)); err != nil {
			return "", fmt.Errorf("failed to navigate: %w", err)
		}
		domContentLoaded, err := client.Page.DOMContentEventFired(ctx)
		if err != nil {
			logger.Warn("DOMContentEventFired failed", zap.Error(err))
		} else {
			defer domContentLoaded.Close()
			_, _ = domContentLoaded.Recv()
		}
	}

	nodeID, err := b.querySelector(ctx, client, selector)
	if err != nil {
		return "", fmt.Errorf("failed to find element: %w", err)
	}

	box, err := client.DOM.GetBoxModel(ctx, &dom.GetBoxModelArgs{
		NodeID: &nodeID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get element box: %w", err)
	}

	if len(box.Model.Content) < 8 {
		return "", fmt.Errorf("invalid box model")
	}

	x := (box.Model.Content[0] + box.Model.Content[4]) / 2
	y := (box.Model.Content[1] + box.Model.Content[5]) / 2

	err = client.Input.DispatchMouseEvent(ctx, input.NewDispatchMouseEventArgs(
		"mousePressed",
		float64(x), float64(y),
	))
	if err != nil {
		return "", fmt.Errorf("failed to press mouse: %w", err)
	}

	err = client.Input.DispatchMouseEvent(ctx, input.NewDispatchMouseEventArgs(
		"mouseReleased",
		float64(x), float64(y),
	))
	if err != nil {
		return "", fmt.Errorf("failed to release mouse: %w", err)
	}

	return fmt.Sprintf("Successfully clicked element: %s", selector), nil
}

// BrowserFillInput Fill input field
func (b *BrowserTool) BrowserFillInput(ctx context.Context, params map[string]interface{}) (string, error) {
	urlStr := ""
	selector, ok := params["selector"].(string)
	if !ok {
		return "", fmt.Errorf("selector parameter is required")
	}

	value, ok := params["value"].(string)
	if !ok {
		return "", fmt.Errorf("value parameter is required")
	}

	if u, ok := params["url"].(string); ok {
		urlStr = u
	}

	logger.Debug("Browser filling input", zap.String("url", urlStr), zap.String("selector", selector), zap.String("value", "***"))

	sessionMgr, err := b.ensureSession(params, false, urlStr != "", false, true)
	if err != nil {
		return "", err
	}

	client, err := sessionMgr.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get browser client: %w", err)
	}

	if urlStr != "" {
		if _, err := client.Page.Navigate(ctx, page.NewNavigateArgs(urlStr)); err != nil {
			return "", fmt.Errorf("failed to navigate: %w", err)
		}
		domContentLoaded, err := client.Page.DOMContentEventFired(ctx)
		if err != nil {
			logger.Warn("DOMContentEventFired failed", zap.Error(err))
		} else {
			defer domContentLoaded.Close()
			_, _ = domContentLoaded.Recv()
		}
	}

	nodeID, err := b.querySelector(ctx, client, selector)
	if err != nil {
		return "", fmt.Errorf("failed to find element: %w", err)
	}

	_ = client.DOM.Focus(ctx, &dom.FocusArgs{
		NodeID: &nodeID,
	})

	script := fmt.Sprintf(`
		(function() {
			var selector = %q;
			var element = document.querySelector(selector);
			if (!element) throw new Error('Element not found');
			var nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
			nativeInputValueSetter.call(element, %q);
			element.dispatchEvent(new Event('input', { bubbles: true }));
			element.dispatchEvent(new Event('change', { bubbles: true }));
		})()
	`, selector, value)

	_, err = client.Runtime.Evaluate(ctx, runtime.NewEvaluateArgs(script))
	if err != nil {
		return "", fmt.Errorf("failed to fill input: %w", err)
	}

	return fmt.Sprintf("Successfully filled input: %s", selector), nil
}

// BrowserGetText Get page text content
func (b *BrowserTool) BrowserGetText(ctx context.Context, params map[string]interface{}) (string, error) {
	urlStr, ok := params["url"].(string)
	if !ok {
		return "", fmt.Errorf("url parameter is required")
	}

	logger.Debug("Browser getting text", zap.String("url", urlStr))

	sessionMgr, err := b.ensureSession(params, true, true, false, true)
	if err != nil {
		return "", err
	}

	client, err := sessionMgr.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get browser client: %w", err)
	}

	nav, err := client.Page.Navigate(ctx, page.NewNavigateArgs(urlStr))
	if err != nil {
		return "", fmt.Errorf("failed to navigate: %w", err)
	}

	domContentLoaded, err := client.Page.DOMContentEventFired(ctx)
	if err != nil {
		logger.Warn("DOMContentEventFired failed", zap.Error(err))
	} else {
		defer domContentLoaded.Close()
		if _, err := domContentLoaded.Recv(); err != nil {
			logger.Warn("WaitForLoadEventFired failed, continuing anyway", zap.Error(err))
		}
	}

	doc, err := client.DOM.GetDocument(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get document: %w", err)
	}

	html, err := client.DOM.GetOuterHTML(ctx, &dom.GetOuterHTMLArgs{
		NodeID: &doc.Root.NodeID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get outer HTML: %w", err)
	}

	text := htmlToText(html.OuterHTML)
	if len(text) > 10000 {
		text = text[:10000] + "\n\n... (truncated)"
	}

	return fmt.Sprintf(
		"Page text from %s\nFrame ID: %s\nWindow mode: %s\n\n%s",
		urlStr,
		string(nav.FrameID),
		b.sessionModeLabel(sessionMgr),
		text,
	), nil
}

// querySelector Find element using CSS selector and return node ID
func (b *BrowserTool) querySelector(ctx context.Context, client *cdp.Client, selector string) (dom.NodeID, error) {
	doc, err := client.DOM.GetDocument(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get document: %w", err)
	}

	result, err := client.DOM.QuerySelector(ctx, &dom.QuerySelectorArgs{
		NodeID:   doc.Root.NodeID,
		Selector: selector,
	})
	if err != nil {
		return 0, fmt.Errorf("query selector failed: %w", err)
	}

	if result.NodeID == 0 {
		return 0, fmt.Errorf("element not found: %s", selector)
	}

	return result.NodeID, nil
}

// GetTools Get all browser tools
func (b *BrowserTool) GetTools() []Tool {
	return []Tool{
		NewBaseTool(
			"browser_navigate",
			"Open or navigate a browser page. Use this when the user explicitly wants a page opened for viewing or interaction.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to navigate to (must start with http:// or https://)",
					},
					"window_mode": browserWindowModeProperty("if no session exists, defaults to interactive and opens a visible Chrome window"),
				},
				"required": []string{"url"},
			},
			b.BrowserNavigate,
		),
		NewBaseTool(
			"browser_screenshot",
			"Take a screenshot of the current page or of a URL. Good for visual inspection and page capture.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to navigate to before screenshot (optional)",
					},
					"width": map[string]interface{}{
						"type":        "number",
						"description": "Screenshot width in pixels (default: 1920)",
					},
					"height": map[string]interface{}{
						"type":        "number",
						"description": "Screenshot height in pixels (default: 1080)",
					},
					"window_mode": browserWindowModeProperty("if a new session must be created, defaults to headless; if a session already exists, reuses it"),
				},
			},
			b.BrowserScreenshot,
		),
		NewBaseTool(
			"browser_execute_script",
			"Execute JavaScript in the current page. Prefer this for controlled browser automation, not general code execution.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"script": map[string]interface{}{
						"type":        "string",
						"description": "JavaScript code to execute",
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to navigate to before executing (optional)",
					},
					"window_mode": browserWindowModeProperty("if a new session must be created, defaults to headless; if a session already exists, reuses it"),
				},
				"required": []string{"script"},
			},
			b.BrowserExecuteScript,
		),
		NewBaseTool(
			"browser_click",
			"Click an element on the page using a CSS selector. Use for browser interaction workflows.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector of the element to click (e.g., '#button', '.submit', '[name=\"submit\"]')",
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to navigate to before clicking (optional)",
					},
					"window_mode": browserWindowModeProperty("if a new session must be created, defaults to interactive; existing sessions are reused unless overridden"),
				},
				"required": []string{"selector"},
			},
			b.BrowserClick,
		),
		NewBaseTool(
			"browser_fill_input",
			"Fill an input field with text. Use for visible browser interaction or guided form automation.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector of the input field (e.g., '#username', 'input[name=\"search\"]')",
					},
					"value": map[string]interface{}{
						"type":        "string",
						"description": "Text to fill into the input field",
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to navigate to before filling (optional)",
					},
					"window_mode": browserWindowModeProperty("if a new session must be created, defaults to interactive; existing sessions are reused unless overridden"),
				},
				"required": []string{"selector", "value"},
			},
			b.BrowserFillInput,
		),
		NewBaseTool(
			"browser_get_text",
			"Read and return the text content of a web page. Prefer this for information retrieval and summarization.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL of the page to get text from",
					},
					"window_mode": browserWindowModeProperty("if no session exists, defaults to headless for invisible information retrieval"),
				},
				"required": []string{"url"},
			},
			b.BrowserGetText,
		),
	}
}

// htmlToText Convert HTML to plain text
func htmlToText(html string) string {
	text := ""
	inTag := false
	for i := 0; i < len(html); i++ {
		if html[i] == '<' {
			inTag = true
			continue
		}
		if html[i] == '>' {
			inTag = false
			continue
		}
		if !inTag {
			text += string(html[i])
		}
	}
	return text
}

// formatCDPResult Format CDP execution result
func formatCDPResult(result *runtime.RemoteObject) (string, error) {
	if result == nil {
		return "null", nil
	}

	if result.Value != nil {
		s := string(result.Value)
		return s, nil
	}

	if result.Description != nil {
		return *result.Description, nil
	}

	return "", nil
}
