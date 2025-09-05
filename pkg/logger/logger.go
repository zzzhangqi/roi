package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

type Logger struct {
	infoLogger    *log.Logger
	errorLogger   *log.Logger
	debugLogger   *log.Logger
	logFile       *os.File
	consoleOutput bool
}

func NewLogger(consoleOutput bool) (*Logger, error) {
	// 创建日志文件名（按日期-小时分钟命名）
	now := time.Now()
	logFileName := fmt.Sprintf("roi-install-%s.log", now.Format("2006-01-02-15-04"))
	
	// 创建日志文件
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("无法创建日志文件: %w", err)
	}

	var writers []io.Writer
	writers = append(writers, logFile)
	
	if consoleOutput {
		writers = append(writers, os.Stdout)
	}

	multiWriter := io.MultiWriter(writers...)

	logger := &Logger{
		infoLogger:    log.New(multiWriter, "[INFO] ", log.LstdFlags),
		errorLogger:   log.New(multiWriter, "[ERROR] ", log.LstdFlags),
		debugLogger:   log.New(multiWriter, "[DEBUG] ", log.LstdFlags),
		logFile:       logFile,
		consoleOutput: consoleOutput,
	}

	logger.Info("日志记录已启动，详细日志保存到: %s", logFileName)
	return logger, nil
}

func (l *Logger) Info(format string, v ...interface{}) {
	l.infoLogger.Printf(format, v...)
}

func (l *Logger) Error(format string, v ...interface{}) {
	l.errorLogger.Printf(format, v...)
}

func (l *Logger) Debug(format string, v ...interface{}) {
	l.debugLogger.Printf(format, v...)
}

func (l *Logger) Close() error {
	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}