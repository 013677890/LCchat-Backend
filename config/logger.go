package config

// LoggerConfig 定义 zap 日志初始化所需的最小参数集。
// - 默认写入 stdout/stderr，方便容器中用 docker logs 采集。
// - 如需直接写文件，可在 OutputPaths/ErrorOutputPaths 配置路径（无滚动，由外部系统切割）。
type LoggerConfig struct {
	Level            string   `json:"level" yaml:"level"`                       // 日志级别: debug|info|warn|error
	Encoding         string   `json:"encoding" yaml:"encoding"`                 // 编码格式: json 或 console
	Development      bool     `json:"development" yaml:"development"`           // 开发模式: 输出更详细的堆栈/检查
	EnableColor      bool     `json:"enableColor" yaml:"enableColor"`           // console 模式时是否彩色等级
	OutputPaths      []string `json:"outputPaths" yaml:"outputPaths"`           // 普通日志输出，默认 stdout
	ErrorOutputPaths []string `json:"errorOutputPaths" yaml:"errorOutputPaths"` // 错误日志输出，默认 stderr
}

// DefaultLoggerConfig 返回开箱即用的配置：json 编码 + stdout/stderr。
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:            "info",
		Encoding:         "json",
		Development:      false,
		EnableColor:      false,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
}
