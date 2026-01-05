package main

import (
	"log"

	"ChatServer/config"
	"ChatServer/pkg/logger"

	"go.uber.org/zap"
)

// main 演示最基本的日志初始化与输出。
// 运行：go run main.go
func main() {
	// 使用默认配置（json + stdout/stderr）。
	cfg := config.DefaultLoggerConfig()

	// 构建 logger。
	lg, err := logger.Build(cfg)
	if err != nil {
		log.Fatalf("build logger failed: %v", err)
	}
	defer lg.Sync()

	// 替换全局实例，方便 zap.L()/zap.S() 使用。
	logger.ReplaceGlobal(lg)

	// 示例日志。
	logger.L().Info("service started", zap.String("MID","1234567890"),zap.String("env", "dev"))
	logger.L().Warn("sample warning", zap.String("MID","1234567890"),zap.String("module", "logger"))
	logger.L().Error("sample error", zap.String("MID","1234567890"),zap.String("module", "logger"))
}

