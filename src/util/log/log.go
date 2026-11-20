package log

import (
	"fmt"
	"strings"
	"sync"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/natefinch/lumberjack"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"time"
)

var Sugar *zap.SugaredLogger

var (
	atomicLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	levelMu     sync.Mutex
)

func Init() {
	atomicLevel.SetLevel(getLogLevel())
	core := zapcore.NewTee(
		zapcore.NewCore(getConsoleEncoder(), getConsoleWriter(), atomicLevel),
		zapcore.NewCore(getFileEncoder(), getFileWriter(), atomicLevel),
	)
	logger := zap.New(core, zap.AddCaller())
	Sugar = logger.Sugar()
}

func getFileEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	return zapcore.NewJSONEncoder(encoderConfig)
}

func getConsoleEncoder() zapcore.Encoder {
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	// Pad the caller column so messages start at the same position even
	// when filenames and line numbers have different lengths.
	encoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(fmt.Sprintf("%-24s", caller.TrimmedPath()))
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
}

func getFileWriter() zapcore.WriteSyncer {
	file := fmt.Sprintf("%s/log_%s.log",
		config.LogSavePath,
		time.Now().Format("20060102"))
	lumberJackLogger := &lumberjack.Logger{
		Filename:   file,
		MaxSize:    viper.GetInt("log_max_size"),
		MaxBackups: viper.GetInt("max_backups"),
		MaxAge:     viper.GetInt("log_max_age"),
		Compress:   false,
	}
	return zapcore.AddSync(lumberJackLogger)
}

func getConsoleWriter() zapcore.WriteSyncer {
	return zapcore.Lock(os.Stdout)
}

func getLogLevel() zapcore.Level {
	level, _, err := parseLevel(config.LogLevel)
	if err != nil {
		return zapcore.InfoLevel
	}
	return level
}

func SetLevel(level string) error {
	nextLevel, normalized, err := parseLevel(level)
	if err != nil {
		return err
	}

	levelMu.Lock()
	defer levelMu.Unlock()

	currentLevel := atomicLevel.Level()
	current := levelToString(currentLevel)
	if current == normalized {
		return nil
	}

	message := fmt.Sprintf("[log] level changed: %s -> %s", current, normalized)
	if Sugar != nil && currentLevel <= zapcore.WarnLevel {
		Sugar.Warn(message)
	}
	atomicLevel.SetLevel(nextLevel)
	if Sugar != nil && currentLevel > zapcore.WarnLevel {
		Sugar.Warn(message)
	}
	return nil
}

func CurrentLevel() string {
	return levelToString(atomicLevel.Level())
}

func NormalizeLevel(level string) (string, error) {
	_, normalized, err := parseLevel(level)
	return normalized, err
}

func parseLevel(level string) (zapcore.Level, string, error) {
	normalized := strings.ToLower(strings.TrimSpace(level))
	switch normalized {
	case "debug":
		return zapcore.DebugLevel, normalized, nil
	case "info":
		return zapcore.InfoLevel, normalized, nil
	case "warn":
		return zapcore.WarnLevel, normalized, nil
	case "error":
		return zapcore.ErrorLevel, normalized, nil
	default:
		return zapcore.InfoLevel, "", fmt.Errorf("unsupported log level %q", level)
	}
}

func levelToString(level zapcore.Level) string {
	switch level {
	case zapcore.DebugLevel:
		return "debug"
	case zapcore.InfoLevel:
		return "info"
	case zapcore.WarnLevel:
		return "warn"
	case zapcore.ErrorLevel:
		return "error"
	default:
		return level.String()
	}
}
