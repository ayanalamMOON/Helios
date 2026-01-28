package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// LogLevel represents log severity
type LogLevel string

const (
	LevelDebug LogLevel = "DEBUG"
	LevelInfo  LogLevel = "INFO"
	LevelWarn  LogLevel = "WARN"
	LevelError LogLevel = "ERROR"
	LevelFatal LogLevel = "FATAL"
)

// Logger provides structured logging
type Logger struct {
	component string
	level     LogLevel
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     LogLevel               `json:"level"`
	Component string                 `json:"component"`
	Message   string                 `json:"message"`
	TraceID   string                 `json:"trace_id,omitempty"`
	SpanID    string                 `json:"span_id,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

// NewLogger creates a new logger
func NewLogger(component string) *Logger {
	return &Logger{
		component: component,
		level:     LevelInfo,
	}
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
}

// Debug logs a debug message
func (l *Logger) Debug(message string, extra ...map[string]interface{}) {
	if l.shouldLog(LevelDebug) {
		l.log(LevelDebug, message, extra...)
	}
}

// Info logs an info message
func (l *Logger) Info(message string, extra ...map[string]interface{}) {
	if l.shouldLog(LevelInfo) {
		l.log(LevelInfo, message, extra...)
	}
}

// Warn logs a warning message
func (l *Logger) Warn(message string, extra ...map[string]interface{}) {
	if l.shouldLog(LevelWarn) {
		l.log(LevelWarn, message, extra...)
	}
}

// Error logs an error message
func (l *Logger) Error(message string, extra ...map[string]interface{}) {
	if l.shouldLog(LevelError) {
		l.log(LevelError, message, extra...)
	}
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(message string, extra ...map[string]interface{}) {
	l.log(LevelFatal, message, extra...)
	os.Exit(1)
}

func (l *Logger) log(level LogLevel, message string, extra ...map[string]interface{}) {
	entry := LogEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Level:     level,
		Component: l.component,
		Message:   message,
	}

	if len(extra) > 0 && extra[0] != nil {
		entry.Extra = extra[0]
	}

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal log entry: %v\n", err)
		return
	}

	fmt.Println(string(data))
}

func (l *Logger) shouldLog(level LogLevel) bool {
	levels := map[LogLevel]int{
		LevelDebug: 0,
		LevelInfo:  1,
		LevelWarn:  2,
		LevelError: 3,
		LevelFatal: 4,
	}

	return levels[level] >= levels[l.level]
}

// Global logger instance
var globalLogger = NewLogger("helios")

// SetGlobalLevel sets the global log level
func SetGlobalLevel(level LogLevel) {
	globalLogger.SetLevel(level)
}

// Debug logs a debug message using the global logger
func Debug(message string, extra ...map[string]interface{}) {
	globalLogger.Debug(message, extra...)
}

// Info logs an info message using the global logger
func Info(message string, extra ...map[string]interface{}) {
	globalLogger.Info(message, extra...)
}

// Warn logs a warning message using the global logger
func Warn(message string, extra ...map[string]interface{}) {
	globalLogger.Warn(message, extra...)
}

// Error logs an error message using the global logger
func Error(message string, extra ...map[string]interface{}) {
	globalLogger.Error(message, extra...)
}

// Fatal logs a fatal message using the global logger and exits
func Fatal(message string, extra ...map[string]interface{}) {
	globalLogger.Fatal(message, extra...)
}
