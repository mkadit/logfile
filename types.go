// logfile/types.go - Optimized core types with object pooling
package logfile

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"sync"
	"time"
)

// Global variables for the logging system state
var (
	AppLogger   *Loggers
	Config      *LogConfiguration
	loggerMutex sync.RWMutex
	configMutex sync.RWMutex

	logChannel chan LogPayload
	logWorkers sync.WaitGroup

	TimeFormat        = time.DateOnly
	DefaultTimeFormat = time.RFC3339Nano
	Testing           bool

	// NEW: Object pools for reducing allocations
	logPayloadPool = sync.Pool{
		New: func() interface{} {
			return &LogPayload{
				OtherAttrs: make([]any, 0, 8), // Pre-allocate common size
			}
		},
	}

	attrSlicePool = sync.Pool{
		New: func() interface{} {
			slice := make([]slog.Attr, 0, 16) // Pre-allocate common size
			return &slice
		},
	}
)

// LogPayload is a struct to hold all necessary info for a single log entry
type LogPayload struct {
	Logger     *MultLogger
	Level      LogLevel
	EventType  string
	Err        error
	Msg        string
	TimeNow    string
	Ml         *MessageLog
	OtherAttrs []any
}

// NEW: Reset method for pool reuse
func (lp *LogPayload) Reset() {
	lp.Logger = nil
	lp.Level = 0
	lp.EventType = ""
	lp.Err = nil
	lp.Msg = ""
	lp.TimeNow = ""
	lp.Ml = nil
	lp.OtherAttrs = lp.OtherAttrs[:0] // Keep capacity, reset length
}

// NEW: Pool management functions
func getLogPayload() *LogPayload {
	return logPayloadPool.Get().(*LogPayload)
}

func putLogPayload(lp *LogPayload) {
	lp.Reset()
	logPayloadPool.Put(lp)
}

func getAttrSlice() *[]slog.Attr {
	return attrSlicePool.Get().(*[]slog.Attr)
}

func putAttrSlice(slice *[]slog.Attr) {
	*slice = (*slice)[:0] // Reset length, keep capacity
	attrSlicePool.Put(slice)
}

// LogLevel represents different log severity levels
type (
	LogLevel int
	MsgKey   struct{}
)

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelCritical
)

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	case LevelCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func ParseLogLevel(levelStr string) (LogLevel, error) {
	switch levelStr {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	case "critical":
		return LevelCritical, nil
	default:
		return LevelInfo, fmt.Errorf("unknown log level: %s", levelStr)
	}
}

func (l LogLevel) ToSlogLevel() slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError, LevelCritical:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// MultLogger combines standard and structured loggers
type MultLogger struct {
	name             string
	config           FileConfig
	writer           io.Writer
	level            LogLevel
	StructuredLogger *slog.Logger
	StdLogger        *log.Logger

	additionalSlogLoggers map[OutputTarget]*slog.Logger
	additionalStdLoggers  map[OutputTarget]*log.Logger
}

func (m *MultLogger) IsEnabled(level LogLevel) bool {
	return level >= m.level
}

func (m *MultLogger) IsStructuredActive() bool {
	return m.config.SlogWriter
}

func (m *MultLogger) IsStandardActive() bool {
	return m.config.StdWriter
}

// Loggers holds all the different types of loggers
type Loggers struct {
	MessageLogger  *MultLogger
	EventLogger    *MultLogger
	ErrorLogger    *MultLogger
	CriticalLogger *MultLogger
	HTTPLogger     *MultLogger
	NMMLogger      *MultLogger
	DebugLogger    *MultLogger
	IndexLogger    *MultLogger
}

// LogConfiguration holds the entire logging configuration
type LogConfiguration struct {
	General GeneralConfig         `json:"general"`
	Files   map[string]FileConfig `json:"files"`
}

// GeneralConfig holds general logging settings
type GeneralConfig struct {
	LevelsByType             map[string]string `json:"levels_by_type"`
	DevelopmentMode          bool              `json:"development_mode"`
	PrettyPrint              bool              `json:"pretty_print"`
	AddSource                bool              `json:"add_source"`
	EnableCentralizedLogging bool              `json:"enable_centralized_logging"`
	LogChannel               int               `json:"log_channel"`

	// NEW: Performance tuning options
	WorkerPoolSize      int  `json:"worker_pool_size"`      // Base worker count (default: 8)
	MaxWorkerPoolSize   int  `json:"max_worker_pool_size"`  // Max worker count (default: 32)
	EnableObjectPooling bool `json:"enable_object_pooling"` // Enable sync.Pool (default: true)
}

// OutputTarget specifies a target log type
type OutputTarget struct {
	Type string `json:"type"`
}

// AdditionalOutputConfig defines additional output targets
type AdditionalOutputConfig struct {
	SlogOutputs []OutputTarget `json:"slog_outputs"`
	StdOutputs  []OutputTarget `json:"std_outputs"`
}

// FileConfig holds detailed settings for an individual log file
type FileConfig struct {
	Path       string `json:"path"`
	MaxSizeMB  int    `json:"max_size_mb"`
	MaxBackups int    `json:"max_backups"`
	MaxAgeDays int    `json:"max_age_days"`
	Compress   bool   `json:"compress"`
	MinLevel   string `json:"min_level"`

	Structured    bool `json:"structured"`
	StdWriter     bool `json:"std_writer"`
	SlogWriter    bool `json:"slog_writer"`
	ConsoleStd    bool `json:"console_std"`
	UseFileWriter bool `json:"use_file_writer"`

	AdditionalOutputs AdditionalOutputConfig `json:"additional_outputs"`
}
