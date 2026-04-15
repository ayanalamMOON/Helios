package plugin

import (
	"fmt"
	"log"
	"os"
)

// StdLogger writes plugin logs to stdout/stderr via the standard logger.
type StdLogger struct {
	component string
	logger    *log.Logger
}

// NewStdLogger creates a simple logger suitable for plugin manager bootstrap.
func NewStdLogger(component string) *StdLogger {
	if component == "" {
		component = "plugin"
	}
	return &StdLogger{
		component: component,
		logger:    log.New(os.Stdout, "", log.LstdFlags),
	}
}

func (l *StdLogger) Debug(message string, fields map[string]interface{}) {
	l.output("DEBUG", message, fields)
}

func (l *StdLogger) Info(message string, fields map[string]interface{}) {
	l.output("INFO", message, fields)
}

func (l *StdLogger) Warn(message string, fields map[string]interface{}) {
	l.output("WARN", message, fields)
}

func (l *StdLogger) Error(message string, fields map[string]interface{}) {
	l.output("ERROR", message, fields)
}

func (l *StdLogger) output(level, message string, fields map[string]interface{}) {
	if l == nil || l.logger == nil {
		return
	}
	if len(fields) == 0 {
		l.logger.Printf("[%s][%s] %s", level, l.component, message)
		return
	}
	l.logger.Printf("[%s][%s] %s | %s", level, l.component, message, flattenFields(fields))
}

func flattenFields(fields map[string]interface{}) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return fmt.Sprintf("%v", parts)
}
