package logger

import (
	"io"
	"log"
	"os"
	"sync"
)

var (
	instance *Logger
	once     sync.Once
)

type Logger struct {
	info  *log.Logger
	error *log.Logger
	debug *log.Logger
}

func Init(logPath string, debug bool) *Logger {
	once.Do(func() {
		var writers []io.Writer
		writers = append(writers, os.Stdout)

		if logPath != "" {
			if err := os.MkdirAll("logs", 0755); err == nil {
				if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
					writers = append(writers, f)
				}
			}
		}

		multi := io.MultiWriter(writers...)
		instance = &Logger{
			info:  log.New(multi, "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile),
			error: log.New(multi, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile),
			debug: log.New(multi, "[DEBUG] ", log.Ldate|log.Ltime|log.Lshortfile),
		}
	})
	return instance
}

func Get() *Logger {
	if instance == nil {
		return Init("", false)
	}
	return instance
}

func (l *Logger) Info(format string, v ...interface{}) {
	l.info.Printf(format, v...)
}

func (l *Logger) Error(format string, v ...interface{}) {
	l.error.Printf(format, v...)
}

func (l *Logger) Debug(format string, v ...interface{}) {
	l.debug.Printf(format, v...)
}

func Info(format string, v ...interface{}) {
	Get().Info(format, v...)
}

func Error(format string, v ...interface{}) {
	Get().Error(format, v...)
}

func Debug(format string, v ...interface{}) {
	Get().Debug(format, v...)
}
