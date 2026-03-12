package logger

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	log         *zap.Logger
	sugar       *zap.SugaredLogger
	logMutex    sync.RWMutex
	once        sync.Once
	initialized bool
)

// LogFileConfig 日志文件配置
type LogFileConfig struct {
	// Path 日志文件完整路径
	Path string
	// SplitByDay 是否按天拆分日志（如 goclaw-2026-03-11.log）
	SplitByDay bool
	// MaxSizeMB 单个文件最大 MB，超出轮转（默认 100）
	MaxSizeMB int
	// MaxBackups 保留旧文件数（默认 7）
	MaxBackups int
	// MaxAgeDays 保留天数，超出自动删除（默认 30）
	MaxAgeDays int
	// Compress 是否压缩归档文件（默认 true）
	Compress bool
}

// rotatingWriter 基于标准库实现的日志轮转写入器
type rotatingWriter struct {
	mu          sync.Mutex
	cfg         *LogFileConfig
	file        *os.File
	currentPath string
	currentDay  string
	currentSize int64
	maxSize     int64 // bytes
}

func newRotatingWriter(cfg *LogFileConfig) (*rotatingWriter, error) {
	maxSize := cfg.MaxSizeMB
	if maxSize <= 0 {
		maxSize = 100
	}
	w := &rotatingWriter{
		cfg:     cfg,
		maxSize: int64(maxSize) * 1024 * 1024,
	}
	if err := w.openFile(); err != nil {
		return nil, err
	}
	// 清理过期日志（启动时执行一次）
	go w.cleanup()
	return w, nil
}

func (w *rotatingWriter) resolvePath(t time.Time) string {
	if !w.cfg.SplitByDay {
		return w.cfg.Path
	}

	dir := filepath.Dir(w.cfg.Path)
	base := filepath.Base(w.cfg.Path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	day := t.Format("2006-01-02")

	if ext == "" {
		return filepath.Join(dir, fmt.Sprintf("%s-%s", stem, day))
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", stem, day, ext))
}

func (w *rotatingWriter) openFile() error {
	path := w.resolvePath(time.Now())
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.currentPath = path
	w.currentDay = time.Now().Format("2006-01-02")
	w.currentSize = info.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 按天切分：日期变化时切换到新文件（不重命名旧文件）
	if w.cfg.SplitByDay {
		today := time.Now().Format("2006-01-02")
		if w.currentDay != "" && w.currentDay != today {
			if err := w.switchDay(); err != nil {
				_ = err // 切换失败继续写当前文件，避免丢日志
			}
		}
	}

	// 检查是否需要按大小轮转
	if w.currentSize+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			// 轮转失败继续写入当前文件，不丢日志
			_ = err
		}
	}

	n, err = w.file.Write(p)
	w.currentSize += int64(n)
	return
}

func (w *rotatingWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// switchDay closes current file and opens today's file when SplitByDay is enabled.
func (w *rotatingWriter) switchDay() error {
	if w.file != nil {
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}
	if err := w.openFile(); err != nil {
		return err
	}
	go w.cleanup()
	return nil
}

// rotate 将当前日志文件重命名为带时间戳的备份，然后新建日志文件
func (w *rotatingWriter) rotate() error {
	currentPath := w.currentPath
	if currentPath == "" {
		currentPath = w.resolvePath(time.Now())
	}

	if w.file != nil {
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}

	timestamp := time.Now().Format("2006-01-02T15-04-05")
	backupPath := fmt.Sprintf("%s.%s", currentPath, timestamp)
	_ = os.Rename(currentPath, backupPath)

	if w.cfg.Compress {
		go compressFile(backupPath)
	}

	go w.cleanup()

	return w.openFile()
}

// compressFile 压缩指定文件为 .gz，压缩完成后删除原文件
func compressFile(path string) {
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()

	dst, err := os.Create(path + ".gz")
	if err != nil {
		return
	}
	defer dst.Close()

	gz := gzip.NewWriter(dst)
	defer gz.Close()

	if _, err := io.Copy(gz, src); err != nil {
		_ = os.Remove(path + ".gz")
		return
	}
	_ = gz.Close()
	_ = dst.Close()
	_ = src.Close()
	_ = os.Remove(path) // 压缩成功后删除原备份
}

// cleanup 删除旧日志：
// - SplitByDay=false: 按 MaxBackups + MaxAgeDays 清理轮转备份（兼容旧行为）
// - SplitByDay=true: 主要按 MaxAgeDays 清理按天文件及其轮转备份
func (w *rotatingWriter) cleanup() {
	dir := filepath.Dir(w.cfg.Path)
	base := filepath.Base(w.cfg.Path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type backupFile struct {
		name    string
		path    string
		modTime time.Time
	}
	var backups []backupFile

	if w.cfg.SplitByDay {
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		prefix := stem + "-"

		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			// 匹配按天日志及其轮转备份，例如：
			// goclaw-2026-03-11.log
			// goclaw-2026-03-11.log.2026-03-11T23-59-59
			// goclaw-2026-03-11.log.2026-03-11T23-59-59.gz
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			if ext != "" && !strings.Contains(name, ext) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backupFile{
				name:    name,
				path:    filepath.Join(dir, name),
				modTime: info.ModTime(),
			})
		}
	} else {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			// 匹配 goclaw.log.2006-... 或 goclaw.log.2006-....gz
			if !strings.HasPrefix(name, base+".") || name == base {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backupFile{
				name:    name,
				path:    filepath.Join(dir, name),
				modTime: info.ModTime(),
			})
		}
	}

	// 按修改时间降序排（最新的在前）
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	maxBackups := w.cfg.MaxBackups
	if maxBackups <= 0 {
		maxBackups = 7
	}
	maxAge := w.cfg.MaxAgeDays
	if maxAge <= 0 {
		maxAge = 30
	}
	cutoff := time.Now().AddDate(0, 0, -maxAge)

	for i, b := range backups {
		if w.cfg.SplitByDay {
			// 按天拆分模式优先按天数保留
			if b.modTime.Before(cutoff) {
				_ = os.Remove(b.path)
			}
			continue
		}

		// 兼容旧行为：超出数量限制 或 超出时间限制 → 删除
		if i >= maxBackups || b.modTime.Before(cutoff) {
			_ = os.Remove(b.path)
		}
	}
}

// Init 初始化日志，只输出到控制台（兼容旧调用）
func Init(level string, development bool) error {
	var initErr error
	once.Do(func() {
		initErr = doInit(level, development, nil)
	})
	return initErr
}

// InitWithFile 初始化日志，同时输出到控制台和文件（支持轮转）
func InitWithFile(level string, development bool, fileCfg *LogFileConfig) error {
	var initErr error
	once.Do(func() {
		initErr = doInit(level, development, fileCfg)
	})
	return initErr
}

func doInit(level string, development bool, fileCfg *LogFileConfig) error {
	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	consoleEncoderCfg := zapcore.EncoderConfig{
		TimeKey:        "T",
		LevelKey:       "L",
		NameKey:        "N",
		CallerKey:      "C",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "M",
		StacktraceKey:  "S",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	fileEncoderCfg := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	levelEnabler := zap.NewAtomicLevelAt(zapLevel)

	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(consoleEncoderCfg),
		zapcore.AddSync(os.Stdout),
		levelEnabler,
	)
	cores := []zapcore.Core{consoleCore}

	if fileCfg != nil && fileCfg.Path != "" {
		rw, err := newRotatingWriter(fileCfg)
		if err != nil {
			return fmt.Errorf("failed to create rotating log writer: %w", err)
		}
		fileCore := zapcore.NewCore(
			zapcore.NewJSONEncoder(fileEncoderCfg),
			zapcore.AddSync(rw),
			levelEnabler,
		)
		cores = append(cores, fileCore)
	}

	newLog := zap.New(
		zapcore.NewTee(cores...),
		zap.AddCaller(),
		zap.AddCallerSkip(1),
	)
	if development {
		newLog = newLog.WithOptions(zap.Development())
	}

	logMutex.Lock()
	log = newLog
	sugar = newLog.Sugar()
	initialized = true
	logMutex.Unlock()

	if fileCfg != nil && fileCfg.Path != "" {
		newLog.WithOptions(zap.WithCaller(false)).Info("Log file initialized",
			zap.String("path", fileCfg.Path),
			zap.Bool("split_by_day", fileCfg.SplitByDay),
			zap.Int("max_size_mb", fileCfg.MaxSizeMB),
			zap.Int("max_backups", fileCfg.MaxBackups),
			zap.Int("max_age_days", fileCfg.MaxAgeDays),
			zap.Bool("compress", fileCfg.Compress),
		)
	}
	return nil
}

func L() *zap.Logger {
	logMutex.RLock()
	if initialized {
		l := log
		logMutex.RUnlock()
		return l
	}
	logMutex.RUnlock()
	_ = Init("info", false)
	logMutex.RLock()
	defer logMutex.RUnlock()
	return log
}

func S() *zap.SugaredLogger {
	logMutex.RLock()
	if initialized {
		s := sugar
		logMutex.RUnlock()
		return s
	}
	logMutex.RUnlock()
	_ = Init("info", false)
	logMutex.RLock()
	defer logMutex.RUnlock()
	return sugar
}

func Sync() error {
	logMutex.RLock()
	defer logMutex.RUnlock()
	if log != nil {
		return log.Sync()
	}
	return nil
}

func With(fields ...zap.Field) *zap.Logger  { return L().With(fields...) }
func Debug(msg string, fields ...zap.Field) { L().Debug(msg, fields...) }
func Info(msg string, fields ...zap.Field)  { L().Info(msg, fields...) }
func Warn(msg string, fields ...zap.Field)  { L().Warn(msg, fields...) }
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }
func Fatal(msg string, fields ...zap.Field) { L().Fatal(msg, fields...) }
