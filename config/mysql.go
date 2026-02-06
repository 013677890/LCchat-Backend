package config

import "time"

// MySQLConfig 描述 MySQL 连接与读写分离（可选）的基础参数。
// 本项目当前读/写同库，可提前预留从库 DSN，后续接入只需改配置。
type MySQLConfig struct {
	// 基础连接
	DSN          string        `json:"dsn" yaml:"dsn"`                   // 主库 DSN（必须）
	ReadOnlyDSNs []string      `json:"readOnlyDsns" yaml:"readOnlyDsns"` // 从库 DSN 列表（可为空，默认回退主库）
	MaxOpenConns int           `json:"maxOpenConns" yaml:"maxOpenConns"` // 最大打开连接数
	MaxIdleConns int           `json:"maxIdleConns" yaml:"maxIdleConns"` // 最大空闲连接数
	ConnMaxIdle  time.Duration `json:"connMaxIdle" yaml:"connMaxIdle"`   // 连接最大空闲时间
	ConnMaxLife  time.Duration `json:"connMaxLife" yaml:"connMaxLife"`   // 连接最长存活时间
	LogLevel     string        `json:"logLevel" yaml:"logLevel"`         // gorm 日志级别: silent|error|warn|info
}

// DefaultMySQLConfig 返回便于本地开发的默认配置：读写同一个 DSN。
func DefaultMySQLConfig() MySQLConfig {
	dsn := getenvString("MYSQL_DSN", "")
	if dsn == "" {
		user := getenvString("MYSQL_USER", "root")
		password := getenvString("MYSQL_PASSWORD", "root")
		host := getenvString("MYSQL_HOST", "mysql")
		port := getenvString("MYSQL_PORT", "3306")
		database := getenvString("MYSQL_DATABASE", "chat_server")
		dsn = user + ":" + password + "@tcp(" + host + ":" + port + ")/" + database + "?charset=utf8mb4&parseTime=True&loc=Local"
	}

	return MySQLConfig{
		// 优先使用环境变量 MYSQL_DSN，其次按 MYSQL_HOST/MYSQL_PORT/... 组装
		DSN:          dsn,
		ReadOnlyDSNs: []string{},
		MaxOpenConns: 50,
		MaxIdleConns: 10,
		ConnMaxIdle:  10 * time.Minute,
		ConnMaxLife:  1 * time.Hour,
		LogLevel:     getenvString("MYSQL_LOG_LEVEL", "warn"),
	}
}
