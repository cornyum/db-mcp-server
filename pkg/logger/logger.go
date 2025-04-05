package logger

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	intLogger "github.com/FreePeak/db-mcp-server/internal/logger"
	"gopkg.in/natefinch/lumberjack.v2"
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
	initialized bool = false
	level       string
	logFile     *os.File
	logWriter   *lumberjack.Logger
)

// Initialize sets up the logger with the specified level
func Initialize(logLevel string) {
	level = logLevel

	// If in stdio mode, redirect logs to a file
	if os.Getenv("TRANSPORT_MODE") == "stdio" {
		// Create logs directory if it doesn't exist
		logsDir := "logs"
		if _, err := os.Stat(logsDir); os.IsNotExist(err) {
			if err := os.Mkdir(logsDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create logs directory: %v\n", err)
			}
		}

		// 设置按天分割的日志文件名
		currentDate := time.Now().Format("2006-01-02")
		logFilePath := filepath.Join(logsDir, fmt.Sprintf("pkg-logger-%s.log", currentDate))

		// 创建支持日志轮转的写入器
		logWriter = &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    maxLogSize, // 单个文件最大尺寸，单位是MB
			MaxBackups: maxBackups, // 最多保留的旧文件数量
			MaxAge:     maxAge,     // 保留旧文件的最大天数
			Compress:   true,       // 是否压缩旧文件
			LocalTime:  true,       // 使用本地时间命名备份文件
		}

		// 关闭之前的日志文件（如果存在）
		if logFile != nil {
			logFile.Close()
		}

		// 使用lumberjack作为底层写入器
		var err error
		logFile, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// Last message to stderr before giving up
			fmt.Fprintf(os.Stderr, "Failed to create pkg logger file: %v\n", err)
		}
	}

	initialized = true

	// 同步初始化内部logger
	intLogger.Initialize(logLevel)
}

// ensureInitialized makes sure the logger is initialized
func ensureInitialized() {
	if !initialized {
		// Default to info level
		Initialize("info")
	}

	// 检查日期是否变更，如果变更则重新初始化日志文件
	if logWriter != nil {
		currentDate := time.Now().Format("2006-01-02")
		expectedFilename := filepath.Join("logs", fmt.Sprintf("pkg-logger-%s.log", currentDate))
		if logWriter.Filename != expectedFilename {
			Initialize(level)
		}
	}
}

// getFormattedCaller returns the file name and line number in "file.go:line" format
func getFormattedCaller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown:0"
	}
	// 只保留文件名，不包括完整路径
	shortFile := filepath.Base(file)
	return fmt.Sprintf("%s:%d", shortFile, line)
}

// writeJSONLog writes a JSON formatted log entry to the log file
func writeJSONLog(level, file, message string, stack string) {
	if logWriter != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		var jsonLog string

		if stack != "" {
			jsonLog = fmt.Sprintf(`{"timestamp":"%s","level":"%s","file":"%s","msg":"%s","stack":"%s"}`+"\n",
				timestamp, level, file, message, stack)
		} else {
			jsonLog = fmt.Sprintf(`{"timestamp":"%s","level":"%s","file":"%s","msg":"%s"}`+"\n",
				timestamp, level, file, message)
		}

		// Write to log file
		if _, err := logWriter.Write([]byte(jsonLog)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write to log file: %v\n", err)
		}
	}
}

// Debug logs a debug message
func Debug(format string, v ...interface{}) {
	ensureInitialized()
	if !shouldLog("debug") {
		return
	}
	file := getFormattedCaller(2) // Skip this function and runtime.Caller
	message := fmt.Sprintf(format, v...)

	// 在stdio模式下直接写入JSON日志
	if os.Getenv("TRANSPORT_MODE") == "stdio" {
		writeJSONLog("DEBUG", file, message, "")
		return
	}

	// 使用内部logger
	intLogger.Debug("%s - %s", file, message)
}

// Info logs an info message
func Info(format string, v ...interface{}) {
	ensureInitialized()
	if !shouldLog("info") {
		return
	}
	file := getFormattedCaller(2) // Skip this function and runtime.Caller
	message := fmt.Sprintf(format, v...)

	// 在stdio模式下直接写入JSON日志
	if os.Getenv("TRANSPORT_MODE") == "stdio" {
		writeJSONLog("INFO", file, message, "")
		return
	}

	// 使用内部logger
	intLogger.Info("%s - %s", file, message)
}

// Warn logs a warning message
func Warn(format string, v ...interface{}) {
	ensureInitialized()
	if !shouldLog("warn") {
		return
	}
	file := getFormattedCaller(2) // Skip this function and runtime.Caller
	message := fmt.Sprintf(format, v...)

	// 在stdio模式下直接写入JSON日志
	if os.Getenv("TRANSPORT_MODE") == "stdio" {
		writeJSONLog("WARN", file, message, "")
		return
	}

	// 使用内部logger
	intLogger.Warn("%s - %s", file, message)
}

// Error logs an error message
func Error(format string, v ...interface{}) {
	ensureInitialized()
	if !shouldLog("error") {
		return
	}
	file := getFormattedCaller(2) // Skip this function and runtime.Caller
	message := fmt.Sprintf(format, v...)

	// 如果在stdio模式下，需要直接处理错误堆栈
	if os.Getenv("TRANSPORT_MODE") == "stdio" {
		stackTrace := debug.Stack()
		encodedStack := base64.StdEncoding.EncodeToString(stackTrace)
		writeJSONLog("ERROR", file, message, encodedStack)
		return
	}

	// 非stdio模式下，通过internal logger传递堆栈信息
	intLogger.Error("%s - %s", file, message)
}

// ErrorWithStack logs an error with a stack trace
func ErrorWithStack(err error) {
	if err == nil {
		return
	}
	ensureInitialized()
	if !shouldLog("error") {
		return
	}

	file := getFormattedCaller(2) // Skip this function and runtime.Caller

	// 如果在stdio模式下，需要直接处理错误堆栈
	if os.Getenv("TRANSPORT_MODE") == "stdio" {
		stackTrace := debug.Stack()
		encodedStack := base64.StdEncoding.EncodeToString(stackTrace)
		writeJSONLog("ERROR", file, err.Error(), encodedStack)
		return
	}

	// 非stdio模式下，使用内部logger记录
	intLogger.ErrorWithStack(err)
}

// shouldLog determines if we should log a message based on the level
func shouldLog(msgLevel string) bool {
	// Always try to use the internal logger first as it's more sophisticated
	// and handles stdio mode properly
	levels := map[string]int{
		"debug": 0,
		"info":  1,
		"warn":  2,
		"error": 3,
	}

	currentLevel := levels[strings.ToLower(level)]
	messageLevel := levels[strings.ToLower(msgLevel)]

	return messageLevel >= currentLevel
}
