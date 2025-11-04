// logfile/types.go - Optimized core types with object pooling
// This file defines all the fundamental data structures and types used throughout the logging system.
package logfile

import (
	"fmt"      // For formatted I/O operations (like printing error messages)
	"io"       // For basic I/O interfaces (like reading and writing data)
	"log"      // Go's standard logging package
	"log/slog" // Go's newer, structured logging package
	"sync"     // For synchronization primitives like mutexes and wait groups
	"time"     // For time-related functions
)

// Global variables that maintain the state of the logging system.
var (
	// AppLogger holds all the specialized logger instances (message, error, http, etc.)
	AppLogger *Loggers

	// Config holds the current logging configuration loaded from file or defaults.
	// It is protected by configMutex.
	Config *LogConfiguration

	// loggerMutex protects concurrent access to AppLogger and its instances,
	// especially during initialization (SetLogger) and shutdown.
	loggerMutex sync.RWMutex

	// configMutex protects concurrent access to the global Config variable.
	configMutex sync.RWMutex

	// logChannel is the buffered channel for asynchronous logging.
	// LogPayloads are sent here and processed by worker goroutines.
	logChannel chan LogPayload

	// logWorkers tracks active worker goroutines.
	// Used during Shutdown to wait for all workers to finish.
	logWorkers sync.WaitGroup

	// scalerDone is a channel used to signal the worker-scaling goroutine to stop.
	scalerDone chan struct{}

	// TimeFormat defines the date format for log file names (YYYY-MM-DD).
	TimeFormat = time.DateOnly

	// DefaultTimeFormat defines the timestamp format in log entries (RFC3339 with nanoseconds).
	DefaultTimeFormat = time.RFC3339Nano

	// Testing flag disables logging when true, useful for unit tests.
	Testing bool

	// logPayloadPool reuses LogPayload structs to avoid repeated allocations
	// and reduce garbage collection pressure.
	logPayloadPool = sync.Pool{
		New: func() interface{} {
			// When pool is empty, create a new LogPayload.
			return &LogPayload{
				// Pre-allocate the slice for attributes to avoid allocations
				// for common cases.
				OtherAttrs: make([]any, 0, 8),
			}
		},
	}

	// attrSlicePool reuses slices of slog.Attr to reduce allocations
	// in the logToSpecificLogger function.
	attrSlicePool = sync.Pool{
		New: func() interface{} {
			// Pre-allocate slice with capacity for 16 attributes.
			slice := make([]slog.Attr, 0, 16)
			return &slice
		},
	}
)

// LogPayload contains all information needed to write a single log entry.
// This struct is designed to work with object pooling for performance.
type LogPayload struct {
	Logger     *MultLogger // The specific logger instance (e.g., ErrorLogger).
	Level      LogLevel    // Severity level (debug, info, warn, error, critical).
	EventType  string      // Category of the event (e.g., "http", "database").
	Err        error       // Associated error, if any.
	Msg        string      // The main log message.
	TimeNow    string      // Pre-formatted timestamp string.
	Ml         *MessageLog // Optional operation context for multi-step operations.
	OtherAttrs []any       // Additional attributes (key-value pairs).
}

// Reset clears all fields of LogPayload so it can be safely returned to the pool.
// This prevents data leakage between log entries.
func (lp *LogPayload) Reset() {
	lp.Logger = nil
	lp.Level = 0
	lp.EventType = ""
	lp.Err = nil
	lp.Msg = ""
	lp.TimeNow = ""
	lp.Ml = nil
	// Reset slice length to 0 but keep the underlying capacity.
	lp.OtherAttrs = lp.OtherAttrs[:0]
}

// getLogPayload retrieves a LogPayload from the pool or creates a new one.
func getLogPayload() *LogPayload {
	return logPayloadPool.Get().(*LogPayload)
}

// putLogPayload returns a LogPayload to the pool for reuse.
func putLogPayload(lp *LogPayload) {
	lp.Reset()
	logPayloadPool.Put(lp)
}

// getAttrSlice retrieves a *[]slog.Attr from the pool.
func getAttrSlice() *[]slog.Attr {
	return attrSlicePool.Get().(*[]slog.Attr)
}

// putAttrSlice returns a *[]slog.Attr to the pool.
func putAttrSlice(slice *[]slog.Attr) {
	*slice = (*slice)[:0] // Clear slice but keep underlying array.
	attrSlicePool.Put(slice)
}

type (
	// LogLevel represents the severity of a log message.
	LogLevel int
	// MsgKey is an empty struct used as a context key (zero memory overhead).
	MsgKey struct{}
)

// Log level constants in order of increasing severity.
const (
	LevelDebug    LogLevel = iota // 0 - Detailed debugging information.
	LevelInfo                     // 1 - General informational messages.
	LevelWarn                     // 2 - Warning messages (potential issues).
	LevelError                    // 3 - Error messages (definite problems).
	LevelCritical                 // 4 - Critical errors (severe problems).
)

// String converts LogLevel to its string representation (e.g., "info", "error").
// Implements the fmt.Stringer interface.
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

// ParseLogLevel converts a log level string (e.g., from a config file) to a LogLevel type.
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
		// Return a safe default (Info) with an error.
		return LevelInfo, fmt.Errorf("unknown log level: %s", levelStr)
	}
}

// ToSlogLevel converts our custom LogLevel to slog's built-in slog.Level.
func (l LogLevel) ToSlogLevel() slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError, LevelCritical:
		// Slog doesn't have a "Critical" level, so we map both to Error.
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// MultLogger combines standard (log.Logger) and structured (slog.Logger) capabilities.
// It manages the primary writer, log level, and additional output destinations.
type MultLogger struct {
	name             string       // Identifier for this logger (e.g., "http", "error").
	config           FileConfig   // Configuration specific to this logger.
	writer           io.Writer    // Primary output destination (e.g., a lumberjack.Logger).
	level            LogLevel     // Minimum severity level to log.
	StructuredLogger *slog.Logger // Structured logger (JSON format).
	StdLogger        *log.Logger  // Standard logger (plain text format).

	// additionalSlogLoggers holds pre-created loggers for redirecting
	// structured logs to other MultLoggers.
	additionalSlogLoggers map[OutputTarget]*slog.Logger
	// additionalStdLoggers holds pre-created loggers for redirecting
	// standard logs to other MultLoggers.
	additionalStdLoggers map[OutputTarget]*log.Logger
}

// IsEnabled checks if a given log level should be logged by this logger.
// Returns true if the level is at or above the logger's configured minimum level.
func (m *MultLogger) IsEnabled(level LogLevel) bool {
	return level >= m.level
}

// IsStructuredActive checks if structured logging (slog) is enabled in the config.
func (m *MultLogger) IsStructuredActive() bool {
	return m.config.SlogWriter
}

// IsStandardActive checks if standard logging (log.Logger) is enabled in the config.
func (m *MultLogger) IsStandardActive() bool {
	return m.config.StdWriter
}

// Loggers is a registry of all specialized logger instances.
// This struct is held in the global AppLogger variable.
type Loggers struct {
	MessageLogger  *MultLogger // General application messages.
	EventLogger    *MultLogger // Application events.
	ErrorLogger    *MultLogger // Error messages.
	CriticalLogger *MultLogger // Critical errors.
	HTTPLogger     *MultLogger // HTTP request/response logs.
	NMMLogger      *MultLogger // Network Management Module logs (custom).
	DebugLogger    *MultLogger // Debug messages.
	IndexLogger    *MultLogger // Centralized log aggregation.
}

// LogConfiguration is the top-level configuration structure, mapping to the config JSON.
type LogConfiguration struct {
	General GeneralConfig         `json:"general"` // System-wide settings.
	Files   map[string]FileConfig `json:"files"`   // Per-logger configurations.
}

// GeneralConfig holds system-wide logging settings.
type GeneralConfig struct {
	// LevelsByType maps logger names (e.g., "http") to their minimum log levels ("info").
	LevelsByType map[string]string `json:"levels_by_type"`

	// DevelopmentMode enables features useful for debugging, like debug logs.
	DevelopmentMode bool `json:"development_mode"`

	// PrettyPrint formats JSON logs with indentation (slower).
	PrettyPrint bool `json:"pretty_print"`

	// AddSource includes file name and line number in logs (expensive).
	AddSource bool `json:"add_source"`

	// EnableCentralizedLogging sends all logs to the IndexLogger.
	EnableCentralizedLogging bool `json:"enable_centralized_logging"`

	// LogChannel is the buffer size for the asynchronous logging channel.
	LogChannel int `json:"log_channel"`

	// WorkerPoolSize is the *initial* number of worker goroutines.
	WorkerPoolSize int `json:"worker_pool_size"`

	// MaxWorkerPoolSize is the maximum number of workers the auto-scaler can create.
	MaxWorkerPoolSize int `json:"max_worker_pool_size"`

	// EnableObjectPooling enables memory reuse via sync.Pool (recommended).
	EnableObjectPooling bool `json:"enable_object_pooling"`
}

// OutputTarget specifies a destination for log redirection.
type OutputTarget struct {
	// Type is the name of the target logger (e.g., "event", "message").
	Type string `json:"type"`
}

// AdditionalOutputConfig defines extra destinations for a logger.
type AdditionalOutputConfig struct {
	SlogOutputs []OutputTarget `json:"slog_outputs"` // Additional structured log destinations.
	StdOutputs  []OutputTarget `json:"std_outputs"`  // Additional standard log destinations.
}

// FileConfig holds configuration for a single log file/logger (e.g., "error" logger).
type FileConfig struct {
	// --- File rotation settings (via lumberjack) ---
	Path       string `json:"path"`         // File path for this log.
	MaxSizeMB  int    `json:"max_size_mb"`  // Max size before rotation (megabytes).
	MaxBackups int    `json:"max_backups"`  // Number of old files to keep.
	MaxAgeDays int    `json:"max_age_days"` // Max days to keep old files (0 = forever).
	Compress   bool   `json:"compress"`     // Compress rotated files with gzip.

	// --- Logging level ---
	MinLevel string `json:"min_level"` // Minimum level to log (e.g., "info", "error").

	// --- Output format and destination settings ---
	Structured    bool `json:"structured"`      // Use JSON format (true) or plain text (false).
	StdWriter     bool `json:"std_writer"`      // Enable standard log.Logger.
	SlogWriter    bool `json:"slog_writer"`     // Enable structured slog.Logger.
	ConsoleStd    bool `json:"console_std"`     // Also write to console/stdout.
	UseFileWriter bool `json:"use_file_writer"` // Write to file (vs. console only).

	// AdditionalOutputs allows this logger to also write to other loggers.
	AdditionalOutputs AdditionalOutputConfig `json:"additional_outputs"`
}
