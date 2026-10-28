package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gookit/color"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	gormlogger "gorm.io/gorm/logger"
)

var (
	once      sync.Once
	zapLogger *zap.Logger
)

// GetLogger 返回全局 zap logger（首次调用时初始化，并发安全）
func GetLogger() *zap.Logger {
	once.Do(initLogger)
	return zapLogger
}

func initLogger() {
	var err error
	zapLogger, err = createLogger()
	if err != nil {
		// 日志系统都起不来时退回标准错误输出，而不是 panic
		fmt.Fprintf(os.Stderr, "init logger failed: %v\n", err)
		zapLogger = zap.NewNop()
	}
	zap.ReplaceGlobals(zapLogger)
}

// 自定义时间格式
func customTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format("2006-01-02 15:04:05"))
}

func createLogger() (*zap.Logger, error) {
	logsDir := "logs"
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(logsDir, 0o755); err != nil {
			return nil, fmt.Errorf("create logs dir: %w", err)
		}
	}

	logPath := filepath.Join(logsDir, "app.log")

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = customTimeEncoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	syncWriter := zapcore.AddSync(&lumberjack.Logger{
		Filename:  logPath,
		MaxSize:   50, // MB
		MaxAge:    30, // days
		LocalTime: true,
	})
	fileCore := zapcore.NewCore(encoder, syncWriter, zapcore.InfoLevel)

	consoleConfig := zap.NewProductionEncoderConfig()
	consoleConfig.EncodeTime = customTimeEncoder
	consoleConfig.EncodeLevel = colorLevelEncoder
	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(consoleConfig),
		zapcore.AddSync(os.Stdout),
		zapcore.InfoLevel,
	)

	return zap.New(zapcore.NewTee(fileCore, consoleCore)), nil
}

/**************************************** 颜色控制 ****************************************/

func colorLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	var clr color.Style
	switch level {
	case zapcore.DebugLevel:
		clr = color.New(color.FgCyan)
	case zapcore.InfoLevel:
		clr = color.New(color.FgGreen)
	case zapcore.WarnLevel:
		clr = color.New(color.FgYellow)
	case zapcore.ErrorLevel, zapcore.PanicLevel, zapcore.FatalLevel:
		clr = color.New(color.FgRed)
	}
	if clr != nil {
		enc.AppendString(clr.Sprint(level.CapitalString()))
		return
	}
	enc.AppendString(level.CapitalString())
}

/**************************************** GORM Logger ****************************************/

// GormLogger 把 GORM 的 SQL 日志接到 zap
type GormLogger struct {
	ZapLogger *zap.Logger
}

func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface { return l }

func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	l.ZapLogger.Sugar().Infof(msg, data...)
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	l.ZapLogger.Sugar().Warnf(msg, data...)
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	l.ZapLogger.Sugar().Errorf(msg, data...)
}

func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()
	if err != nil {
		l.ZapLogger.Sugar().Errorf("[%.2fms] [rows:%v] %s err:%v",
			float64(elapsed.Nanoseconds())/1e6, rows, sql, err)
		return
	}
	l.ZapLogger.Sugar().Debugf("[%.2fms] [rows:%v] %s",
		float64(elapsed.Nanoseconds())/1e6, rows, sql)
}
