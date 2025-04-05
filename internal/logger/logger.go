package logger

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Level represents the severity of a log message
type Level int

const (
	// LevelDebug for detailed troubleshooting
	LevelDebug Level = iota
	// LevelInfo for general operational entries
	LevelInfo
	// LevelWarn for non-critical issues
	LevelWarn
	// LevelError for errors that should be addressed
	LevelError
)

const (
	// 日志文件大小限制：500MB
	maxLogSize = 500 // megabytes
	// 最大保留日志文件数
	maxBackups = 30
	// 最大保留天数
	maxAge = 90 // days
)

var (
	// Default logger
	zapLogger *zap.Logger
	logLevel  Level
	// Flag to indicate if we're in stdio mode
	isStdioMode bool
	// Log file for stdio mode
	stdioLogFile *os.File
	// Mutex to protect log file access
	logMutex sync.Mutex
)

// safeStdioWriter is a writer that ensures no output goes to stdout in stdio mode
type safeStdioWriter struct {
	file *os.File
}

// Write implements io.Writer and filters all output in stdio mode
func (w *safeStdioWriter) Write(p []byte) (n int, err error) {
	// In stdio mode, write to the log file instead of stdout
	logMutex.Lock()
	defer logMutex.Unlock()

	if stdioLogFile != nil {
		return stdioLogFile.Write(p)
	}

	// Last resort: write to stderr, never stdout
	return os.Stderr.Write(p)
}

// Sync implements zapcore.WriteSyncer
func (w *safeStdioWriter) Sync() error {
	logMutex.Lock()
	defer logMutex.Unlock()

	if stdioLogFile != nil {
		return stdioLogFile.Sync()
	}
	return nil
}

// createLogDir ensures the logs directory exists
func createLogDir() (string, error) {
	logsDir := "logs"
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		if err := os.Mkdir(logsDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create logs directory: %v", err)
		}
	}
	return logsDir, nil
}

// createRotatedLogger creates a lumberjack logger for file rotation
func createRotatedLogger(logPath string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    maxLogSize, // 单个文件最大尺寸，单位是MB
		MaxBackups: maxBackups, // 最多保留的旧文件数量
		MaxAge:     maxAge,     // 保留旧文件的最大天数
		Compress:   true,       // 是否压缩旧文件
		LocalTime:  true,       // 使用本地时间命名备份文件
	}
}

// 自定义编码器配置，确保所有日志都使用JSON格式
func createEncoderConfig() zapcore.EncoderConfig {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	// 移除CallerKey和EncodeCaller，我们将使用自定义字段
	encoderConfig.CallerKey = ""
	return encoderConfig
}

// Initialize sets up the logger with the specified level
func Initialize(level string) {
	setLogLevel(level)

	// Check if we're in stdio mode
	transportMode := os.Getenv("TRANSPORT_MODE")
	isStdioMode = transportMode == "stdio"

	// Create logs directory
	logsDir, err := createLogDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}

	// 设置标准日志文件路径
	stdoutLogPath := filepath.Join(logsDir, "stdout.log")
	stderrLogPath := filepath.Join(logsDir, "stderr.log")

	// 创建带有轮转功能的日志写入器
	stdoutWriter := createRotatedLogger(stdoutLogPath)
	stderrWriter := createRotatedLogger(stderrLogPath)

	// 创建多核心日志器，用于同时写入到控制台和文件
	var core zapcore.Core

	if isStdioMode {
		// In stdio mode, we need to avoid ANY JSON output to stdout
		timestamp := time.Now().Format("20060102-150405")
		logFileName := filepath.Join(logsDir, fmt.Sprintf("mcp-logger-%s.log", timestamp))

		// Try to create the log file
		stdioLogFile, err = os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// If we can't create a log file, we'll use a null logger
			fmt.Fprintf(os.Stderr, "Failed to create log file: %v - all logs will be suppressed\n", err)
			zapLogger = zap.NewNop()
			return
		}

		// Write initial log message to stderr only (as a last message before full redirection)
		fmt.Fprintf(os.Stderr, "Stdio mode detected - all logs redirected to: %s\n", logFileName)

		// Create a custom writer that never writes to stdout
		safeWriter := &safeStdioWriter{file: stdioLogFile}

		// 创建JSON编码器
		encoderConfig := createEncoderConfig()
		encoder := zapcore.NewJSONEncoder(encoderConfig)

		// Create core that writes to our safe writer
		core = zapcore.NewCore(encoder, zapcore.AddSync(safeWriter), getZapLevel(logLevel))
	} else {
		// Standard logger initialization for non-stdio mode

		// 创建编码器配置，确保所有日志使用JSON格式
		encoderConfig := createEncoderConfig()

		// 为不同级别创建不同的输出目标
		highPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return lvl >= zapcore.ErrorLevel
		})
		lowPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return lvl < zapcore.ErrorLevel
		})

		// 所有输出都使用JSON格式
		jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)

		// 控制台和文件都使用相同的JSON编码器
		consoleDebugging := zapcore.Lock(os.Stdout)
		consoleErrors := zapcore.Lock(os.Stderr)

		// 组合多个输出目标
		core = zapcore.NewTee(
			zapcore.NewCore(jsonEncoder, consoleDebugging, lowPriority),
			zapcore.NewCore(jsonEncoder, consoleErrors, highPriority),
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(stdoutWriter), lowPriority),
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(stderrWriter), highPriority),
		)
	}

	// 创建Logger，不使用zap内置的caller，我们将手动添加file字段
	zapLogger = zap.New(core)
}

// InitializeWithWriter sets up the logger with the specified level and output writer
func InitializeWithWriter(level string, writer *os.File) {
	setLogLevel(level)

	// Create logs directory
	logsDir, err := createLogDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}

	// 设置标准日志文件路径
	stdoutLogPath := filepath.Join(logsDir, "stdout.log")
	stderrLogPath := filepath.Join(logsDir, "stderr.log")

	// 创建带有轮转功能的日志写入器
	stdoutWriter := createRotatedLogger(stdoutLogPath)
	stderrWriter := createRotatedLogger(stderrLogPath)

	// 创建编码器配置，确保所有日志使用JSON格式
	encoderConfig := createEncoderConfig()

	// 为不同级别创建不同的输出目标
	highPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.ErrorLevel
	})
	lowPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl < zapcore.ErrorLevel
	})

	// 文件输出使用JSON格式
	jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)

	// 组合多个输出目标
	core := zapcore.NewTee(
		zapcore.NewCore(jsonEncoder, zapcore.AddSync(writer), zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return true // 所有日志都写入到提供的writer
		})),
		zapcore.NewCore(jsonEncoder, zapcore.AddSync(stdoutWriter), lowPriority),
		zapcore.NewCore(jsonEncoder, zapcore.AddSync(stderrWriter), highPriority),
	)

	// 创建Logger，不使用zap内置的caller，我们将手动添加file字段
	zapLogger = zap.New(core)
}

// setLogLevel sets the log level from a string
func setLogLevel(level string) {
	switch strings.ToLower(level) {
	case "debug":
		logLevel = LevelDebug
	case "info":
		logLevel = LevelInfo
	case "warn":
		logLevel = LevelWarn
	case "error":
		logLevel = LevelError
	default:
		logLevel = LevelInfo
	}
}

// getZapLevel converts our level to zap.AtomicLevel
func getZapLevel(level Level) zap.AtomicLevel {
	switch level {
	case LevelDebug:
		return zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case LevelInfo:
		return zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case LevelWarn:
		return zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case LevelError:
		return zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	default:
		return zap.NewAtomicLevelAt(zapcore.InfoLevel)
	}
}

// getCallerInfo returns the file, function and line number of the caller
func getCallerInfo(skip int) (string, int) {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown", 0
	}
	return file, line
}

// 获取格式化的调用位置信息: "file.go:line"
func getFormattedCaller(skip int) string {
	file, line := getCallerInfo(skip)
	// 只保留文件名，不需要完整路径
	shortFile := filepath.Base(file)
	return fmt.Sprintf("%s:%d", shortFile, line)
}

// Debug logs a debug message
func Debug(format string, v ...interface{}) {
	if logLevel > LevelDebug {
		return
	}
	msg := fmt.Sprintf(format, v...)
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Debug(msg, zap.String("file", caller))
}

// Info logs an info message
func Info(format string, v ...interface{}) {
	if logLevel > LevelInfo {
		return
	}
	msg := fmt.Sprintf(format, v...)
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Info(msg, zap.String("file", caller))
}

// Warn logs a warning message
func Warn(format string, v ...interface{}) {
	if logLevel > LevelWarn {
		return
	}
	msg := fmt.Sprintf(format, v...)
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Warn(msg, zap.String("file", caller))
}

// Error logs an error message
func Error(format string, v ...interface{}) {
	if logLevel > LevelError {
		return
	}
	msg := fmt.Sprintf(format, v...)
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	// 获取堆栈信息并进行BASE64编码
	stackTrace := debug.Stack()
	encodedStack := base64.StdEncoding.EncodeToString(stackTrace)
	zapLogger.Error(msg, zap.String("file", caller), zap.String("stack", encodedStack))
}

// ErrorWithStack logs an error with a stack trace
func ErrorWithStack(err error) {
	if err == nil {
		return
	}
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	// 获取堆栈信息并进行BASE64编码
	stackTrace := debug.Stack()
	encodedStack := base64.StdEncoding.EncodeToString(stackTrace)
	zapLogger.Error(err.Error(), zap.String("file", caller), zap.String("stack", encodedStack))
}

// RequestLog logs details of an HTTP request
func RequestLog(method, url, sessionID, body string) {
	if logLevel > LevelDebug {
		return
	}
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Debug("HTTP Request",
		zap.String("file", caller),
		zap.String("method", method),
		zap.String("url", url),
		zap.String("sessionID", sessionID),
		zap.String("body", body),
	)
}

// ResponseLog logs details of an HTTP response
func ResponseLog(statusCode int, sessionID, body string) {
	if logLevel > LevelDebug {
		return
	}
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Debug("HTTP Response",
		zap.String("file", caller),
		zap.Int("statusCode", statusCode),
		zap.String("sessionID", sessionID),
		zap.String("body", body),
	)
}

// SSEEventLog logs details of an SSE event
func SSEEventLog(eventType, sessionID, data string) {
	if logLevel > LevelDebug {
		return
	}
	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Debug("SSE Event",
		zap.String("file", caller),
		zap.String("eventType", eventType),
		zap.String("sessionID", sessionID),
		zap.String("data", data),
	)
}

// RequestResponseLog logs a combined request and response log entry
func RequestResponseLog(method, sessionID string, requestData, responseData string) {
	if logLevel > LevelDebug {
		return
	}

	// Format for more readable logs
	formattedRequest := requestData
	formattedResponse := responseData

	// Try to format JSON if it's valid
	if strings.HasPrefix(requestData, "{") || strings.HasPrefix(requestData, "[") {
		var obj interface{}
		if err := json.Unmarshal([]byte(requestData), &obj); err == nil {
			if formatted, err := json.MarshalIndent(obj, "", "  "); err == nil {
				formattedRequest = string(formatted)
			}
		}
	}

	if strings.HasPrefix(responseData, "{") || strings.HasPrefix(responseData, "[") {
		var obj interface{}
		if err := json.Unmarshal([]byte(responseData), &obj); err == nil {
			if formatted, err := json.MarshalIndent(obj, "", "  "); err == nil {
				formattedResponse = string(formatted)
			}
		}
	}

	// 获取调用者信息，skip=2跳过当前函数和runtime.Caller
	caller := getFormattedCaller(2)
	zapLogger.Debug("Request/Response",
		zap.String("file", caller),
		zap.String("method", method),
		zap.String("sessionID", sessionID),
		zap.String("request", formattedRequest),
		zap.String("response", formattedResponse),
	)
}
