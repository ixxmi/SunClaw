package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/emulation"
	"github.com/mafredri/cdp/protocol/network"
	"github.com/mafredri/cdp/rpcc"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// ConnectionMode 浏览器连接模式
type ConnectionMode string

const (
	ModeAuto   ConnectionMode = "auto"   // 自动检测（优先尝试 relay，失败则尝试 direct）
	ModeDirect ConnectionMode = "direct" // 直接 CDP 连接
	ModeRelay  ConnectionMode = "relay"  // 通过 OpenClaw Relay 连接
)

// BrowserSessionManager 浏览器会话管理器 (使用 Chrome DevTools Protocol 或 OpenClaw Relay)
type BrowserSessionManager struct {
	mu             sync.RWMutex
	devt           *devtool.DevTools
	client         *cdp.Client
	conn           *rpcc.Conn
	cmd            *exec.Cmd
	ready          bool
	headless       bool
	chromePath     string
	userDataDir    string
	remoteURL      string               // 远程 Chrome 实例 URL
	connectionMode ConnectionMode       // 连接模式
	relayURL       string               // OpenClaw Relay URL
	relaySession   *RelaySessionManager // Relay 会话
}

var sessionManager *BrowserSessionManager

// GetBrowserSession 获取浏览器会话管理器（单例）
func GetBrowserSession() *BrowserSessionManager {
	if sessionManager == nil {
		sessionManager = &BrowserSessionManager{}
	}
	return sessionManager
}

// Start 启动浏览器会话
func (b *BrowserSessionManager) Start(timeout time.Duration) error {
	return b.StartWithPreferences(timeout, "", ModeAuto, true)
}

// StartWithMode 使用指定模式启动浏览器会话
func (b *BrowserSessionManager) StartWithMode(timeout time.Duration, relayURL string, mode ConnectionMode) error {
	return b.StartWithPreferences(timeout, relayURL, mode, true)
}

// StartWithPreferences 使用指定模式和窗口可见性启动浏览器会话
func (b *BrowserSessionManager) StartWithPreferences(timeout time.Duration, relayURL string, mode ConnectionMode, headless bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	requestedMode := mode
	if !headless {
		// 可见窗口必须走本地 direct 模式。
		requestedMode = ModeDirect
	}

	if b.ready {
		sameMode := false
		switch requestedMode {
		case ModeAuto:
			sameMode = b.connectionMode == ModeDirect || b.connectionMode == ModeRelay
		default:
			sameMode = b.connectionMode == requestedMode
		}

		if sameMode && b.headless == headless {
			return nil
		}

		logger.Info("Restarting browser session to satisfy requested mode",
			zap.String("current_mode", string(b.connectionMode)),
			zap.Bool("current_headless", b.headless),
			zap.String("requested_mode", string(requestedMode)),
			zap.Bool("requested_headless", headless))
		b.stopLocked()
	}

	b.relayURL = relayURL
	b.headless = headless

	// 根据模式决定连接方式
	switch requestedMode {
	case ModeRelay:
		return b.startRelayMode(timeout)
	case ModeDirect:
		return b.startDirectMode(timeout, headless)
	case ModeAuto:
		// 自动模式：优先尝试 relay，失败则尝试 direct
		if relayURL != "" {
			logger.Debug("Auto mode: trying OpenClaw Relay first")
			err := b.startRelayMode(timeout)
			if err == nil {
				return nil
			}
			logger.Warn("OpenClaw Relay connection failed, falling back to direct CDP", zap.Error(err))
		}
		return b.startDirectMode(timeout, headless)
	default:
		return fmt.Errorf("unknown connection mode: %s", requestedMode)
	}
}

// startRelayMode 启动 Relay 模式
func (b *BrowserSessionManager) startRelayMode(timeout time.Duration) error {
	logger.Debug("Starting browser session with OpenClaw Relay",
		zap.String("relay_url", b.relayURL))

	relaySession := GetRelaySession()
	if err := relaySession.Start(b.relayURL, timeout); err != nil {
		return fmt.Errorf("failed to start relay session: %w", err)
	}

	b.relaySession = relaySession
	b.connectionMode = ModeRelay
	b.ready = true
	logger.Debug("Browser session started successfully with OpenClaw Relay")
	return nil
}

// startDirectMode 启动直接 CDP 模式
func (b *BrowserSessionManager) startDirectMode(timeout time.Duration, headless bool) error {
	logger.Debug("Starting persistent browser session with Chrome DevTools Protocol",
		zap.Bool("headless", headless))

	// 无头模式可以复用现有远程调试会话；可见模式必须启动受控本地窗口。
	if headless {
		if err := b.tryConnectToExisting(); err == nil {
			b.connectionMode = ModeDirect
			b.ready = true
			logger.Debug("Connected to existing Chrome instance")
			return nil
		}
	}

	logger.Debug("No compatible Chrome instance found, starting new instance")

	// 查找 Chrome 可执行文件
	chromePath, err := b.findChrome()
	if err != nil {
		return fmt.Errorf("failed to find Chrome: %w", err)
	}
	b.chromePath = chromePath

	// 创建用户数据目录
	userDataDir, err := os.MkdirTemp("", "goclaw-chrome-")
	if err != nil {
		return fmt.Errorf("failed to create user data dir: %w", err)
	}
	b.userDataDir = userDataDir

	args := []string{
		"--remote-debugging-port=9222",
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
	}
	if headless {
		args = append(args,
			"--headless=new",
			"--no-sandbox",
			"--disable-setuid-sandbox",
			"--disable-dev-shm-usage",
			"--disable-gpu",
			"--disable-software-rasterizer",
			"--disable-background-timer-throttling",
			"--disable-backgrounding-occluded-windows",
			"--disable-renderer-backgrounding",
		)
	} else {
		args = append(args, "--new-window", "about:blank")
	}

	// 启动 Chrome
	b.cmd = exec.Command(chromePath, args...)

	if err := b.cmd.Start(); err != nil {
		os.RemoveAll(userDataDir)
		return fmt.Errorf("failed to start Chrome: %w", err)
	}

	// 等待 Chrome 启动
	select {
	case <-time.After(timeout):
		_ = b.cmd.Process.Kill()
		os.RemoveAll(userDataDir)
		return fmt.Errorf("Chrome did not start within timeout")
	case <-time.After(3 * time.Second):
		// 继续连接
	}

	// 连接到 Chrome
	if err := b.connect(9222); err != nil {
		_ = b.cmd.Process.Kill()
		os.RemoveAll(userDataDir)
		return fmt.Errorf("failed to connect to Chrome: %w", err)
	}

	b.remoteURL = "http://localhost:9222"
	b.connectionMode = ModeDirect
	b.ready = true
	logger.Debug("Browser session started successfully with Chrome DevTools Protocol")
	return nil
}

// tryConnectToExisting 尝试连接到已运行的 Chrome 实例
func (b *BrowserSessionManager) tryConnectToExisting() error {
	// 尝试连接默认端口
	for _, port := range []int{9222, 9223, 9224} {
		if err := b.connect(port); err == nil {
			b.remoteURL = fmt.Sprintf("http://localhost:%d", port)
			return nil
		}
	}
	return fmt.Errorf("no existing Chrome instance found")
}

// connect 连接到指定端口的 Chrome 实例
func (b *BrowserSessionManager) connect(port int) error {
	// 使用 devtool 包
	b.devt = devtool.New(fmt.Sprintf("http://localhost:%d", port))

	// 列出可用的页面
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pt, err := b.devt.Get(ctx, devtool.Page)
	if err != nil {
		// 如果没有页面，创建新标签页
		pt, err = b.devt.Create(ctx)
		if err != nil {
			return fmt.Errorf("failed to create page: %w", err)
		}
	}

	// 连接到 WebSocket
	conn, err := rpcc.DialContext(ctx, pt.WebSocketDebuggerURL)
	if err != nil {
		return fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	b.conn = conn

	// 创建 CDP 客户端
	b.client = cdp.NewClient(conn)

	// 启用需要的域
	if err := b.client.DOM.Enable(ctx); err != nil {
		return fmt.Errorf("failed to enable DOM: %w", err)
	}
	if err := b.client.Page.Enable(ctx); err != nil {
		return fmt.Errorf("failed to enable Page: %w", err)
	}
	if err := b.client.Runtime.Enable(ctx); err != nil {
		return fmt.Errorf("failed to enable Runtime: %w", err)
	}
	if err := b.client.Network.Enable(ctx, network.NewEnableArgs()); err != nil {
		return fmt.Errorf("failed to enable Network: %w", err)
	}

	// 设置真实的 User-Agent 以避免被检测为自动化工具
	// 使用最新 Chrome 的 User-Agent
	userAgent := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	if err := b.client.Emulation.SetUserAgentOverride(ctx, emulation.NewSetUserAgentOverrideArgs(userAgent)); err != nil {
		logger.Warn("Failed to set User-Agent", zap.Error(err))
	}

	return nil
}

// findChrome 查找 Chrome 可执行文件
func (b *BrowserSessionManager) findChrome() (string, error) {
	// 常见 Chrome 路径
	paths := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/mnt/c/Program Files/Google/Chrome/Application/chrome.exe", // WSL
		"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// 尝试通过 which/google-chrome 命令查找
	for _, cmd := range []string{"google-chrome", "google-chrome-stable", "chromium-browser", "chromium", "chrome"} {
		if path, err := exec.LookPath(cmd); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("Chrome not found in common locations")
}

// IsReady 检查会话是否就绪
func (b *BrowserSessionManager) IsReady() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ready
}

// GetClient 获取 CDP 客户端
func (b *BrowserSessionManager) GetClient() (*cdp.Client, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.ready {
		return nil, fmt.Errorf("browser session not ready")
	}

	return b.client, nil
}

// GetRelayClient 获取 Relay 客户端
func (b *BrowserSessionManager) GetRelayClient() *RelaySessionManager {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.relaySession
}

// GetConnectionMode 获取当前连接模式
func (b *BrowserSessionManager) GetConnectionMode() ConnectionMode {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connectionMode
}

// IsHeadless 检查当前会话是否为无头模式
func (b *BrowserSessionManager) IsHeadless() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.headless
}

// IsRelayMode 检查是否使用 Relay 模式
func (b *BrowserSessionManager) IsRelayMode() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.relaySession != nil && b.relaySession.IsReady()
}

// Stop 停止浏览器会话
func (b *BrowserSessionManager) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.ready {
		b.stopLocked()
	}
}

func (b *BrowserSessionManager) stopLocked() {
	logger.Debug("Stopping browser session")

	// 停止 Relay 会话
	if b.relaySession != nil {
		b.relaySession.Stop()
		b.relaySession = nil
	}

	// 关闭连接
	if b.conn != nil {
		_ = b.conn.Close()
	}

	// 停止 Chrome 进程
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_ = b.cmd.Wait()
	}

	// 清理临时目录
	if b.userDataDir != "" {
		_ = os.RemoveAll(b.userDataDir)
	}

	b.ready = false
	b.client = nil
	b.conn = nil
	b.cmd = nil
	b.userDataDir = ""
	b.remoteURL = ""
	b.connectionMode = ModeAuto
	b.relayURL = ""
	b.headless = false
}
