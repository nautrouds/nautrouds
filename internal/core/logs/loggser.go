package logs

import (
	"os"
	"strconv"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Out *zap.Logger = zap.NewNop()

func InitLogger(logLevelStr string) {
	logPath := getEnv("NAUTROUDS_LOG_FILE_PATH", "")
	stdFormat := getEnv("NAUTROUDS_LOG_STD_FORMAT", "")
	fileFormat := getEnv("NAUTROUDS_LOG_FILE_FORMAT", "")

	maxSize, sizeErr := strconv.Atoi(getEnv("NAUTROUDS_LOG_MAX_SIZE", "50"))
	if sizeErr != nil {
		maxSize = 50
	}
	maxBackups, backupsErr := strconv.Atoi(getEnv("NAUTROUDS_LOG_MAX_BACKUPS", "3"))
	if backupsErr != nil {
		maxBackups = 3
	}
	maxAge, ageErr := strconv.Atoi(getEnv("NAUTROUDS_LOG_MAX_AGE", "28"))
	if ageErr != nil {
		maxAge = 28
	}
	compress, compressErr := strconv.ParseBool(getEnv("NAUTROUDS_LOG_COMPRESS", "true"))
	if compressErr != nil {
		compress = true
	}

	level := zapcore.InfoLevel
	if len(logLevelStr) > 0 {
		if err := level.UnmarshalText([]byte(logLevelStr)); err != nil {
			level = zapcore.InfoLevel
		}
	}

	var cores []zapcore.Core

	baseConfig := zap.NewProductionEncoderConfig()
	baseConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	baseConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	consoleConfig := baseConfig
	if stdFormat == "console" {
		consoleConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	consoleEncoder := getEncoder(stdFormat, consoleConfig)
	cores = append(cores, zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level))

	// 2. File Core: JSON output with rotation (optional)
	if logPath != "" {
		fileWriter := zapcore.AddSync(&lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
			Compress:   compress,
		})
		fileEncoder := getEncoder(fileFormat, baseConfig)
		cores = append(cores, zapcore.NewCore(fileEncoder, fileWriter, level))
	}

	combinedCore := zapcore.NewTee(cores...)
	Out = zap.New(combinedCore)

	if sizeErr != nil {
		Out.Error("Invalid NAUTROUDS_LOG_MAX_SIZE", zap.Error(sizeErr))
	}
	if backupsErr != nil {
		Out.Error("Invalid NAUTROUDS_LOG_MAX_BACKUPS", zap.Error(backupsErr))
	}
	if ageErr != nil {
		Out.Error("Invalid NAUTROUDS_LOG_MAX_AGE", zap.Error(ageErr))
	}
	if compressErr != nil {
		Out.Error("Invalid NAUTROUDS_LOG_COMPRESS", zap.Error(compressErr))
	}
}

func Sync() {
	_ = Out.Sync()
}

func getEncoder(format string, config zapcore.EncoderConfig) zapcore.Encoder {
	if format == "json" {
		return zapcore.NewJSONEncoder(config)
	}
	return zapcore.NewConsoleEncoder(config)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
