// logfile/config.go - Optimized configuration
// This file handles all configuration settings for the logging system.
package logfile

import (
	"encoding/json" // Used for parsing JSON configuration files
	"fmt"           // Used for formatted error messages
	"os"            // Used for reading configuration files from disk
)

// Global configuration variables that can be accessed throughout the package.
var (
	// LogAsync determines if logging operations should be non-blocking (asynchronous).
	// This variable is not actually used; async is passed as a parameter to log functions.
	LogAsync = false

	// SystemName is a general identifier for the application using this logger.
	// It appears in log entries (via MessageLog) to identify which system generated the log.
	SystemName = "APPLICATION"
)

// Table legend for mapping writer configurations to output formats.
// This table helps you understand which combination of settings produces which output.
//
// Configuration flags explained:
// - structured: When true, enables JSON output format; when false, uses plain text
// - std_writer: Enables the standard Go log.Logger for writing
// - slog_writer: Enables the structured slog.Logger for writing
// - console_std: When true, also outputs to console/stdout
// - use_file_writer: When true, writes to the configured log file
//
// | structured | std_writer | slog_writer | console_std | File Output
// | :--------- | :--------- | :---------- | :---------- | :--------------------------------
// | `false`    | `false`    | `false`     | `false`     | **NONE** (No writer enabled)
// | `false`    | `false`    | `false`     | `true`      | **NONE** (No writer enabled)
// | `false`    | `false`    | `true`      | `false`     | **Plain Text** (via slog.Logger)
// | `false`    | `false`    | `true`      | `true`      | **Plain Text** (via slog.Logger)
// | `false`    | `true`     | `false`     | `false`     | **Plain Text** (via log.Logger)
// | `false`    | `true`     | `false`     | `true`      | **Plain Text** (via log.Logger)
// | `false`    | `true`     | `true`      | `false`     | **Plain Text** (log.Logger) +
// |            |            |             |             | **Plain Text** (slog.Logger)
// | `false`    | `true`     | `true`      | `true`      | **Plain Text** (log.Logger) +
// |            |            |             |             | **Plain Text** (slog.Logger)
// | `true`     | `false`    | `false`     | `false`     | **NONE** (No writer enabled)
// | `true`     | `false`    | `false`     | `true`      | **NONE** (No writer enabled)
// | `true`     | `false`    | `true`      | `false`     | **JSON** (via slog.Logger)
// | `true`     | `false`    | `true`      | `true`      | **JSON** (via slog.Logger)
// | `true`     | `true`     | `false`     | `false`     | **Plain Text** (via log.Logger)
// | `true`     | `true`     | `false`     | `true`      | **Plain Text** (via log.Logger)
// | `true`     | `true`     | `true`      | `false`     | **Plain Text** (log.Logger) +
// |            |            |             |             | **JSON** (slog.Logger)
// | `true`     | `true`     | `true`      | `true`      | **Plain Text** (log.Logger) +
// |            |            |             |             | **JSON** (slog.Logger)

// DefaultLogConfiguration returns a production-ready logging configuration.
// This provides sensible defaults optimized for performance.
func DefaultLogConfiguration() LogConfiguration {
	return LogConfiguration{
		General: GeneralConfig{
			// LevelsByType defines the minimum log level for each logger type.
			LevelsByType: map[string]string{
				"index":    "info",     // Central log
				"debug":    "debug",    // Debug log (often disabled in prod by DevelopmentMode)
				"message":  "info",     // General app messages
				"event":    "info",     // App events
				"http":     "info",     // HTTP traffic
				"error":    "error",    // Errors only
				"critical": "critical", // Critical errors only
				"nmm":      "info",     // Custom logger
			},

			// DevelopmentMode should be false in production.
			DevelopmentMode: false,

			// PrettyPrint formats JSON (slower), should be false in production.
			PrettyPrint: false,

			// AddSource adds file:line (expensive), should be false in production.
			AddSource: false,

			// EnableCentralizedLogging sends all logs to the "index" logger.
			EnableCentralizedLogging: true,

			// LogChannel is the buffer size for the async log channel.
			LogChannel: 20000,

			// WorkerPoolSize is the base number of goroutines processing logs.
			WorkerPoolSize: 8,

			// MaxWorkerPoolSize is the max number of workers under load.
			MaxWorkerPoolSize: 32,

			// EnableObjectPooling reuses memory to reduce GC (recommended).
			EnableObjectPooling: true,
		},

		// Files defines configuration for each individual log file.
		Files: map[string]FileConfig{
			// Index logger: Central aggregation point.
			// Configured to *not* write to a file, only to console.
			"index": {
				Path:          "logs/Index/Index.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,  // JSON
				StdWriter:     false, // No standard logger
				SlogWriter:    true,  // Use structured logger
				ConsoleStd:    true,  // Output to console
				UseFileWriter: false, // *Do not* write to file
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},

			// Debug logger: Detailed debugging information.
			"debug": {
				Path:          "logs/Debug/Debug.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     false,
				SlogWriter:    true,
				ConsoleStd:    false, // No console output
				UseFileWriter: true,  // Write to file
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},

			// Event logger: Application events.
			"event": {
				Path:          "logs/event/Event.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false, // Plain text
				StdWriter:     true,  // Use standard logger
				SlogWriter:    false, // No structured logger
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},

			// Message logger: General application messages.
			"message": {
				Path:          "logs/Message/Message.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     true,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},

			// Error logger: Errors, also redirects to event and message logs.
			"error": {
				Path:          "logs/error/Error.log",
				MinLevel:      "debug", // Log all levels to file
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     true,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					// Redirect standard output to event and message logs.
					StdOutputs: []OutputTarget{
						{Type: "event"},
						{Type: "message"},
					},
				},
			},

			// Critical logger: Critical errors, redirects to message/error and console.
			"critical": {
				Path:          "logs/critical/Critical.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     true,
				SlogWriter:    false,
				ConsoleStd:    true, // Also show critical errors on console.
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					// Critical errors also go to message and error logs.
					StdOutputs: []OutputTarget{
						{Type: "message"},
						{Type: "error"},
					},
				},
			},

			// HTTP logger: HTTP request/response logs.
			"http": {
				Path:          "logs/http/HTTP.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     true,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},

			// NMM logger: Custom logger.
			"nmm": {
				Path:          "logs/NMM/NMM.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     true,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
		},
	}
}

// TestLogConfiguration returns a configuration optimized for testing.
// It typically disables file writing and central logging to avoid noise.
func TestLogConfiguration() LogConfiguration {
	return LogConfiguration{
		General: GeneralConfig{
			LevelsByType: map[string]string{
				"index":    "debug",
				"debug":    "debug",
				"message":  "info",
				"event":    "info",
				"http":     "info",
				"error":    "error",
				"critical": "critical",
				"nmm":      "info",
			},
			DevelopmentMode:          false,
			PrettyPrint:              false,
			AddSource:                false,
			EnableCentralizedLogging: false, // Disabled for testing
			LogChannel:               10000,
			WorkerPoolSize:           4, // Fewer workers for tests
			MaxWorkerPoolSize:        16,
			EnableObjectPooling:      true,
		},
		Files: map[string]FileConfig{
			// Index logger in test mode is completely disabled.
			"index": {
				Path:          "logs/Index/Index.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false, // No file writing
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			// (Other logger configs would be inherited from default,
			// but this function returns a full struct)
		},
	}
}

// LoadConfigurationFromFile loads logging configuration from a JSON file.
// This allows customizing logging behavior without recompiling the application.
func LoadConfigurationFromFile(filePath string) error {
	// Read the entire file into memory.
	file, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read configuration file: %w", err)
	}

	var loadedConfig LogConfiguration

	// Parse the JSON data into our configuration struct.
	if err := json.Unmarshal(file, &loadedConfig); err != nil {
		return fmt.Errorf("failed to unmarshal configuration JSON: %w", err)
	}

	// Thread-safe update of the global configuration.
	loggerMutex.Lock()
	defer loggerMutex.Unlock()

	Config = &loadedConfig
	return nil
}

// GetLogLevel retrieves the current log level for a specific logger type.
// This is useful for checking if a level is enabled before doing expensive work.
func GetLogLevel(logType string) (LogLevel, error) {
	if AppLogger == nil {
		return LevelInfo, fmt.Errorf("logging system not initialized")
	}

	// Use read lock for thread-safe access to AppLogger.
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	var targetLogger *MultLogger
	switch logType {
	case "message":
		targetLogger = AppLogger.MessageLogger
	case "event":
		targetLogger = AppLogger.EventLogger
	case "error":
		targetLogger = AppLogger.ErrorLogger
	case "critical":
		targetLogger = AppLogger.CriticalLogger
	case "http":
		targetLogger = AppLogger.HTTPLogger
	case "nmm":
		targetLogger = AppLogger.NMMLogger
	case "debug":
		targetLogger = AppLogger.DebugLogger
	default:
		return LevelInfo, fmt.Errorf("unknown logger type: %s", logType)
	}

	if targetLogger == nil {
		return LevelInfo, fmt.Errorf("logger type '%s' is not initialized", logType)
	}

	// Return the logger's configured minimum level.
	return targetLogger.level, nil
}

// getConfigValue safely retrieves the current global configuration
// using a read lock.
func getConfigValue() *LogConfiguration {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return Config
}
