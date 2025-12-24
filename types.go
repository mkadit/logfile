// logfile/types.go - Optimized core types with object pooling
package logfile

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Global variables that maintain the state of the logging system.
var (
	// AppLogger holds all the specialized logger instances (e.g., MessageLogger, ErrorLogger).
	// It is protected by loggerMutex.
	AppLogger *Loggers

	// Config holds the current logging configuration.
	// It is an atomic.Value to allow for thread-safe hot-reloads of the config.
	Config atomic.Value // Stores a *LogConfiguration

	// loggerMutex protects AppLogger during initialization and rotation.
	// RLock is used by logging functions to access AppLogger safely.
	loggerMutex sync.RWMutex

	// logChannel is the buffered channel for asynchronous logging.
	// LogPayloads are sent here and processed by worker goroutines.
	// Protected by channelMutex.
	logChannel   chan LogPayload
	channelMutex sync.RWMutex

	// logWorkers is a WaitGroup that tracks all active worker goroutines
	// and the autoscaler goroutine. Used for graceful shutdown.
	logWorkers sync.WaitGroup

	// scalerDone signals the worker-scaling goroutine to stop.
	// Protected by scalerMutex.
	scalerDone  chan struct{}
	scalerMutex sync.RWMutex

	// shutdownOnce ensures the Shutdown() function logic runs only once.
	shutdownOnce sync.Once

	// isShuttingDown is an atomic flag to prevent new log operations
	// during the shutdown process.
	isShuttingDown atomic.Bool

	// TimeFormat is the format used for dated log file names (e.g., "2023-10-27").
	TimeFormat = time.DateOnly
	// DefaultTimeFormat is the format used for timestamps within log messages.
	DefaultTimeFormat = time.RFC3339Nano
	// Testing is a flag to modify behavior during tests (e.g., Fatal logs don't os.Exit).
	Testing bool

	// logPayloadPool holds reusable LogPayload objects to reduce allocations.
	logPayloadPool = sync.Pool{
		New: func() interface{} {
			return &LogPayload{
				OtherAttrs: make([]any, 0, 8), // Pre-allocate slice capacity
			}
		},
	}

	// attrSlicePool holds reusable *[]slog.Attr slices to reduce allocations.
	attrSlicePool = sync.Pool{
		New: func() interface{} {
			slice := make([]slog.Attr, 0, 16) // Pre-allocate slice capacity
			return &slice
		},
	}
)

// LogPayload contains all information needed to write a single log entry.
// This struct is sent through the logChannel for async logging.
type LogPayload struct {
	Logger     *MultLogger // The specific logger (e.g., ErrorLogger) to use.
	Level      LogLevel
	EventType  string
	Err        error
	Msg        string
	TimeNow    string      // Pre-formatted timestamp.
	Ml         *MessageLog // IMPORTANT: Should be a Clone for async logging.
	OtherAttrs []any       // IMPORTANT: Should be deep-copied for async logging.
}

// Reset clears all fields so the LogPayload can be safely returned to the pool.
func (lp *LogPayload) Reset() {
	lp.Logger = nil
	lp.Level = 0
	lp.EventType = ""
	lp.Err = nil
	lp.Msg = ""
	lp.TimeNow = ""
	lp.Ml = nil
	// Clear slice but keep underlying capacity
	if lp.OtherAttrs != nil {
		lp.OtherAttrs = lp.OtherAttrs[:0]
	}
}

// getLogPayload retrieves a LogPayload from the pool or creates a new one.
func getLogPayload() *LogPayload {
	return logPayloadPool.Get().(*LogPayload)
}

// putLogPayload returns a LogPayload to the pool for reuse.
func putLogPayload(lp *LogPayload) {
	if lp == nil {
		return
	}
	lp.Reset()
	logPayloadPool.Put(lp)
}

// getAttrSlice retrieves a *[]slog.Attr from the pool.
func getAttrSlice() *[]slog.Attr {
	return attrSlicePool.Get().(*[]slog.Attr)
}

// putAttrSlice returns a *[]slog.Attr to the pool.
func putAttrSlice(slice *[]slog.Attr) {
	if slice == nil {
		return
	}
	// Clear slice but keep underlying capacity
	*slice = (*slice)[:0]
	attrSlicePool.Put(slice)
}

type (
	// LogLevel defines the severity of a log message.
	LogLevel int
	// MsgKey is an empty struct used as a context key,
	// though it doesn't appear to be used in the provided files.
	MsgKey struct{}
)

// LogLevel constants.
const (
	LevelDebug    LogLevel = iota // 0
	LevelInfo                     // 1
	LevelWarn                     // 2
	LevelError                    // 3
	LevelCritical                 // 4
)

// String returns a string representation of the log level.
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

// ParseLogLevel converts a string (e.g., from config) into a LogLevel.
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
		// Default to Info on unknown level
		return LevelInfo, fmt.Errorf("unknown log level: %s", levelStr)
	}
}

// ToSlogLevel converts our custom LogLevel to the standard log/slog.Level.
func (l LogLevel) ToSlogLevel() slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError, LevelCritical: // Both map to slog.LevelError
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// MultLogger combines standard (log.Logger) and structured (slog.Logger)
// logging capabilities into a single unit.
type MultLogger struct {
	name                  string                        // The type of logger (e.g., "error", "message").
	config                FileConfig                    // The specific configuration for this logger.
	writer                io.Writer                     // The primary writer (e.g., lumberjack.Logger).
	level                 LogLevel                      // The minimum level this logger will write.
	StructuredLogger      *slog.Logger                  // The structured logger instance.
	StdLogger             *log.Logger                   // The standard logger instance.
	additionalSlogLoggers map[OutputTarget]*slog.Logger // For forwarding logs
	additionalStdLoggers  map[OutputTarget]*log.Logger  // For forwarding logs
}

// IsEnabled checks if the logger is configured to write logs at the given level.
func (m *MultLogger) IsEnabled(level LogLevel) bool {
	return level >= m.level
}

// IsStructuredActive checks if the slog writer is configured to be used.
func (m *MultLogger) IsStructuredActive() bool {
	return m.config.SlogWriter
}

// IsStandardActive checks if the standard log writer is configured to be used.
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
	IndexLogger    *MultLogger // The centralized logger
}

// LogConfiguration is the top-level struct for the JSON config file.
type LogConfiguration struct {
	General GeneralConfig         `json:"general" yaml:"general" xml:"general"`
	Files   map[string]FileConfig `json:"files" yaml:"files" xml:"files"`
}

// GeneralConfig holds global logging settings.
type GeneralConfig struct {
	LevelsByType             map[string]string `json:"levels_by_type" yaml:"levels_by_type" xml:"levels_by_type"`                                     // Fallback log levels
	DevelopmentMode          bool              `json:"development_mode" yaml:"development_mode" xml:"development_mode"`                               // Enables debug logging
	PrettyPrint              bool              `json:"pretty_print" yaml:"pretty_print" xml:"pretty_print"`                                           // Use colorful console output
	AddSource                bool              `json:"add_source" yaml:"add_source" xml:"add_source"`                                                 // Log file:line (performance cost)
	EnableCentralizedLogging bool              `json:"enable_centralized_logging" yaml:"enable_centralized_logging" xml:"enable_centralized_logging"` // Forward logs to IndexLogger
	LogChannel               int               `json:"log_channel" yaml:"log_channel" xml:"log_channel"`                                              // Buffer size for async channel
	WorkerPoolSize           int               `json:"worker_pool_size" yaml:"worker_pool_size" xml:"worker_pool_size"`                               // Initial worker goroutines
	MaxWorkerPoolSize        int               `json:"max_worker_pool_size" yaml:"max_worker_pool_size" xml:"max_worker_pool_size"`                   // Max worker goroutines
	EnableObjectPooling      bool              `json:"enable_object_pooling" yaml:"enable_object_pooling" xml:"enable_object_pooling"`                // Use sync.Pool for LogPayloads
}

// OutputTarget defines a logger type to forward logs to.
type OutputTarget struct {
	Type string `json:"type" yaml:"type" xml:"type"` // e.g., "message", "error"
}

// AdditionalOutputConfig specifies other loggers to forward logs to.
type AdditionalOutputConfig struct {
	SlogOutputs []OutputTarget `json:"slog_outputs" yaml:"slog_outputs" xml:"slog_outputs"`
	StdOutputs  []OutputTarget `json:"std_outputs" yaml:"std_outputs" xml:"std_outputs"`
}

// FileConfig holds settings for a specific logger type (e.g., "error").
type FileConfig struct {
	Path              string                 `json:"path" yaml:"path" xml:"path"`                                           // Log file path
	MaxSizeMB         int                    `json:"max_size_mb" yaml:"max_size_mb" xml:"max_size_mb"`                      // For rotation
	MaxBackups        int                    `json:"max_backups" yaml:"max_backups" xml:"max_backups"`                      // For rotation
	MaxAgeDays        int                    `json:"max_age_days" yaml:"max_age_days" xml:"max_age_days"`                   // For rotation
	Compress          bool                   `json:"compress" yaml:"compress" xml:"compress"`                               // For rotation
	MinLevel          string                 `json:"min_level" yaml:"min_level" xml:"min_level"`                            // e.g., "debug", "info"
	Structured        bool                   `json:"structured" yaml:"structured" xml:"structured"`                         // Unused? SlogWriter seems to control this
	StdWriter         bool                   `json:"std_writer" yaml:"std_writer" xml:"std_writer"`                         // Enable standard log.Logger
	SlogWriter        bool                   `json:"slog_writer" yaml:"slog_writer" xml:"slog_writer"`                      // Enable structured slog.Logger
	ConsoleStd        bool                   `json:"console_std" yaml:"console_std" xml:"console_std"`                      // Echo logs to os.Stderr
	UseFileWriter     bool                   `json:"use_file_writer" yaml:"use_file_writer" xml:"use_file_writer"`          // Write to file (true) or io.Discard (false)
	AdditionalOutputs AdditionalOutputConfig `json:"additional_outputs" yaml:"additional_outputs" xml:"additional_outputs"` // Log forwarding
}

// --- Helper functions for safe access to global state ---

// getLogChannelSafe safely retrieves the log channel.
// It uses RLock and checks the shutdown flag.
func getLogChannelSafe() (chan LogPayload, bool) {
	channelMutex.RLock()
	defer channelMutex.RUnlock()

	if isShuttingDown.Load() {
		return nil, false
	}
	return logChannel, logChannel != nil
}

// getScalerDoneSafe safely retrieves the scaler done channel.
func getScalerDoneSafe() (chan struct{}, bool) {
	scalerMutex.RLock()
	defer scalerMutex.RUnlock()
	return scalerDone, scalerDone != nil
}

// isLoggerActive checks if the logging system is initialized and not shutting down.
func isLoggerActive() bool {
	return !isShuttingDown.Load() && AppLogger != nil
}
