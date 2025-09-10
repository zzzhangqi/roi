package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

type Logger struct {
	fileLogger      *log.Logger
	consoleLogger   *log.Logger
	logFile         *os.File
	logFilePath     string   // 日志文件路径
	consoleLevel    LogLevel // 控制台输出级别
	fileLevel       LogLevel // 文件输出级别
	suppressConsole bool     // 是否抑制控制台输出（进度条模式）
}

// NewLogger 创建新的日志记录器
// consoleLevel: 控制台输出级别 (ERROR表示只显示错误，INFO表示显示所有)
// fileLevel: 文件输出级别 (通常为DEBUG，记录所有详细信息)
func NewLogger(consoleLevel, fileLevel LogLevel) (*Logger, error) {
	// 创建日志文件名（按日期-小时分钟命名）
	now := time.Now()
	logFileName := fmt.Sprintf("roi-install-%s.log", now.Format("2006-01-02-15-04"))
	
	// 创建日志文件
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("无法创建日志文件: %w", err)
	}

	logger := &Logger{
		fileLogger:      log.New(logFile, "", log.LstdFlags),
		consoleLogger:   log.New(os.Stdout, "", log.LstdFlags),
		logFile:         logFile,
		logFilePath:     logFileName,
		consoleLevel:    consoleLevel,
		fileLevel:       fileLevel,
		suppressConsole: false,
	}

	// 只在文件中记录启动信息
	logger.fileLogger.Printf("[INFO] 日志记录已启动，详细日志保存到: %s", logFileName)
	return logger, nil
}

// NewProgressLogger 创建适用于进度显示的日志记录器
// 控制台只显示ERROR级别，所有详细信息记录到文件
func NewProgressLogger() (*Logger, error) {
	return NewLogger(ERROR, DEBUG)
}

// SuppressConsole 抑制控制台输出（进度条模式）
func (l *Logger) SuppressConsole() {
	l.suppressConsole = true
}

// EnableConsole 启用控制台输出
func (l *Logger) EnableConsole() {
	l.suppressConsole = false
}

func (l *Logger) log(level LogLevel, levelName string, format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	
	// 始终写入文件（如果级别足够）
	if level >= l.fileLevel {
		l.fileLogger.Printf("[%s] %s", levelName, message)
	}
	
	// 根据设置决定是否写入控制台
	if !l.suppressConsole && level >= l.consoleLevel {
		l.consoleLogger.Printf("[%s] %s", levelName, message)
	}
}

func (l *Logger) Debug(format string, v ...interface{}) {
	l.log(DEBUG, "DEBUG", format, v...)
}

func (l *Logger) Info(format string, v ...interface{}) {
	l.log(INFO, "INFO", format, v...)
}

func (l *Logger) Warn(format string, v ...interface{}) {
	l.log(WARN, "WARN", format, v...)
}

func (l *Logger) Error(format string, v ...interface{}) {
	l.log(ERROR, "ERROR", format, v...)
}

// InfoToFileOnly 只输出到文件的信息日志
func (l *Logger) InfoToFileOnly(format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	l.fileLogger.Printf("[INFO] %s", message)
}

// InfoToConsoleOnly 只输出到控制台的信息日志（用于进度显示）
func (l *Logger) InfoToConsoleOnly(format string, v ...interface{}) {
	if !l.suppressConsole {
		message := fmt.Sprintf(format, v...)
		l.consoleLogger.Printf("[INFO] %s", message)
	}
}

// GetLogFilePath 返回当前日志文件的路径
func (l *Logger) GetLogFilePath() string {
	return l.logFilePath
}

func (l *Logger) Close() error {
	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}