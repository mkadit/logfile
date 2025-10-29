// logfile/types.go - Core types and global variables for the logging system
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
	AppLogger   *Loggers          // AppLogger is the main logger instance for the application
	Config      *LogConfiguration // Config holds the loaded logging configuration
	loggerMutex sync.RWMutex      // loggerMutex ensures thread-safe logger operations and configuration changes
	configMutex sync.RWMutex

	// New variables for asynchronous logging
	logChannel chan LogPayload // A channel to hold log entries for async processing
	logWorkers sync.WaitGroup  // A WaitGroup to ensure all workers finish before shutdown

	TimeFormat        = time.DateOnly    // TimeFormat defines the date format used for log files
	DefaultTimeFormat = time.RFC3339Nano // A default robust time format for structured logs (e.g., "2006-01-02T15:04:05.999999999Z07:00")
	Testing           bool               // Testing indicates if we're in test mode (suppresses logging to actual files sometimes)
)

// LogPayload is a struct to hold all necessary info for a single log entry,
// used for passing log requests to the async channel.
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

// LogLevel represents different log severity levels (e.g., Debug, Info, Error)
type (
	LogLevel int
	MsgKey   struct{}
)

const (
	LevelDebug    LogLevel = iota // 0
	LevelInfo                     // 1
	LevelWarn                     // 2
	LevelError                    // 3
	LevelCritical                 // 4
)

// String returns the string representation of a LogLevel.
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

// ParseLogLevel converts a string to a LogLevel.
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

// ToSlogLevel converts LogLevel to slog.Level.
func (l LogLevel) ToSlogLevel() slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError, LevelCritical: // Map critical/fatal/index to slog.LevelError for consistency
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// MultLogger combines standard (log.Logger) and structured (slog.Logger) loggers
// along with their configuration and primary writer.
type MultLogger struct {
	name             string
	config           FileConfig
	writer           io.Writer // The primary writer for this logger (e.g., lumberjack.Logger)
	level            LogLevel  // Minimum level for this logger
	StructuredLogger *slog.Logger
	StdLogger        *log.Logger

	// New: Pre-created loggers for additional outputs
	additionalSlogLoggers map[OutputTarget]*slog.Logger // Map target type to slog.Logger
	additionalStdLoggers  map[OutputTarget]*log.Logger  // Map target type to log.Logger
}

// IsEnabled checks if a given log level is enabled for this MultLogger.
func (m *MultLogger) IsEnabled(level LogLevel) bool {
	return level >= m.level
}

// IsStructuredActive returns true if the structured logger (slog) is active for this MultLogger.
func (m *MultLogger) IsStructuredActive() bool {
	return m.config.SlogWriter
}

// IsStandardActive returns true if the standard logger (log) is active for this MultLogger.
func (m *MultLogger) IsStandardActive() bool {
	return m.config.StdWriter
}

// Loggers holds all the different types of loggers used by the application.
type Loggers struct {
	MessageLogger  *MultLogger
	EventLogger    *MultLogger
	ErrorLogger    *MultLogger
	CriticalLogger *MultLogger
	HTTPLogger     *MultLogger
	NMMLogger      *MultLogger
	DebugLogger    *MultLogger
	IndexLogger    *MultLogger // New: Centralized index logger
}

// LogConfiguration holds the entire logging configuration loaded from a file.
type LogConfiguration struct {
	General GeneralConfig         `json:"general"`
	Files   map[string]FileConfig `json:"files"`
}

// GeneralConfig holds general logging settings that apply across the system.
type GeneralConfig struct {
	LevelsByType             map[string]string `json:"levels_by_type"` // Defines default level per type if not specified by file
	DevelopmentMode          bool              `json:"development_mode"`
	PrettyPrint              bool              `json:"pretty_print"`
	AddSource                bool              `json:"add_source"`
	EnableCentralizedLogging bool              `json:"enable_centralized_logging"` // New: Enable/disable centralized logging
	LogChannel               int               `json:"log_channel"`
}

// OutputTarget specifies a target log type for additional outputs.
type OutputTarget struct {
	Type string `json:"type"` // The log type, e.g., "debug", "event", "message"
}

// AdditionalOutputConfig defines where slog-formatted or std-formatted logs should also be sent.
type AdditionalOutputConfig struct {
	SlogOutputs []OutputTarget `json:"slog_outputs"` // List of log types to which the slog-formatted output of THIS log type should be sent
	StdOutputs  []OutputTarget `json:"std_outputs"`  // List of log types to which the std-formatted output of THIS log type should be sent
}

// FileConfig holds detailed settings for an individual log file and its behavior.
type FileConfig struct {
	Path       string `json:"path"`         // Full path to the log file (e.g., "logs/Error/Error.log").
	MaxSizeMB  int    `json:"max_size_mb"`  // Maximum size of a log file before rotation, in megabytes.
	MaxBackups int    `json:"max_backups"`  // Maximum number of old (rotated) log files to keep.
	MaxAgeDays int    `json:"max_age_days"` // Maximum number of days to retain old log files (0 means no age limit).
	Compress   bool   `json:"compress"`     // Whether to compress old log files (e.g., .gz).
	MinLevel   string `json:"min_level"`    // New: Minimum log level for this specific file

	// File format and writer configuration
	Structured    bool `json:"structured"`      // If true, file uses JSON format; if false, uses text format (relevant for slog)
	StdWriter     bool `json:"std_writer"`      // If true, uses standard log.Logger for file writing
	SlogWriter    bool `json:"slog_writer"`     // If true, uses slog.Logger for file writing
	ConsoleStd    bool `json:"console_std"`     // If true, this logger's output also goes to standard console (stdout/stderr)
	UseFileWriter bool `json:"use_file_writer"` // If true, uses lumberjack file writer; if false, only writes to stderr

	// Additional outputs specific to this log type's format
	AdditionalOutputs AdditionalOutputConfig `json:"additional_outputs"`
}
