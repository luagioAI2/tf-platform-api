package logger

import (
	"go.uber.org/zap"
)

// 所有方法都通过 GetLogger() 获取实例，避免初始化顺序导致的 nil panic
// （原实现直接使用包级变量，在 GetLogger() 之前调用会 panic）。

func Debug(args ...interface{})  { GetLogger().Sugar().Debug(args...) }
func Info(args ...interface{})   { GetLogger().Sugar().Info(args...) }
func Warn(args ...interface{})   { GetLogger().Sugar().Warn(args...) }
func Error(args ...interface{})  { GetLogger().Sugar().Error(args...) }
func DPanic(args ...interface{}) { GetLogger().Sugar().DPanic(args...) }
func Panic(args ...interface{})  { GetLogger().Sugar().Panic(args...) }
func Fatal(args ...interface{})  { GetLogger().Sugar().Fatal(args...) }

func Debugf(msg string, args ...interface{})  { GetLogger().Sugar().Debugf(msg, args...) }
func Infof(msg string, args ...interface{})   { GetLogger().Sugar().Infof(msg, args...) }
func Warnf(msg string, args ...interface{})   { GetLogger().Sugar().Warnf(msg, args...) }
func Errorf(msg string, args ...interface{})  { GetLogger().Sugar().Errorf(msg, args...) }
func DPanicf(msg string, args ...interface{}) { GetLogger().Sugar().DPanicf(msg, args...) }
func Panicf(msg string, args ...interface{})  { GetLogger().Sugar().Panicf(msg, args...) }
func Fatalf(msg string, args ...interface{})  { GetLogger().Sugar().Fatalf(msg, args...) }

func Debugw(msg string, keysAndValues ...interface{}) {
	GetLogger().Sugar().Debugw(msg, keysAndValues...)
}
func Infow(msg string, keysAndValues ...interface{}) {
	GetLogger().Sugar().Infow(msg, keysAndValues...)
}
func Warnw(msg string, keysAndValues ...interface{}) {
	GetLogger().Sugar().Warnw(msg, keysAndValues...)
}
func Errorw(msg string, keysAndValues ...interface{}) {
	GetLogger().Sugar().Errorw(msg, keysAndValues...)
}

// Printf 兼容旧调用
func Printf(msg string, args ...interface{}) { GetLogger().Sugar().Infof(msg, args...) }

func ZDebug(msg string, fields ...zap.Field) { GetLogger().Debug(msg, fields...) }
func ZInfo(msg string, fields ...zap.Field)  { GetLogger().Info(msg, fields...) }
func ZWarn(msg string, fields ...zap.Field)  { GetLogger().Warn(msg, fields...) }
func ZError(msg string, fields ...zap.Field) { GetLogger().Error(msg, fields...) }
func ZDPanic(msg string, fields ...zap.Field) {
	GetLogger().DPanic(msg, fields...)
}
func ZPanic(msg string, fields ...zap.Field) { GetLogger().Panic(msg, fields...) }
func ZFatal(msg string, fields ...zap.Field) { GetLogger().Fatal(msg, fields...) }
