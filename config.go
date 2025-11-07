// logfile/config.go - Optimized configuration
package logfile

import (
	"encoding/json"
	"fmt"
	"os"
)

var (
	// LogAsync controls whether logging is asynchronous (true) or synchronous (false).
	// This is a global flag, but the standard logging functions (Info, Error, etc.)
	// also take an 'async' parameter for per-call control.
	LogAsync = false

	// SystemName is a global identifier for the application, used in standard log messages.
	SystemName = "APPLICATION"
)

// DefaultLogConfiguration returns a production-ready logging configuration.
// This configuration is used as a fallback if loading from a file fails
// or if no configuration is explicitly set before CreateLogger is called.
func DefaultLogConfiguration() LogConfiguration {
	return LogConfiguration{
		General: GeneralConfig{
			LevelsByType: map[string]string{
				"index":    "info",
				"debug":    "debug",
				"message":  "info",
				"event":    "info",
				"http":     "info",
				"error":    "error",
				"critical": "critical",
				"nmm":      "info",
			},
			DevelopmentMode:          false, // Disable debug logging by default
			PrettyPrint:              false, // Use structured JSON for production
			AddSource:                false, // AddSource can have a performance impact
			EnableCentralizedLogging: true,  // Send logs to the 'index' logger
			LogChannel:               1000,  // Large buffer for async logging
			WorkerPoolSize:           8,     // Start with 8 worker goroutines
			MaxWorkerPoolSize:        32,    // Allow scaling up to 32 workers
			EnableObjectPooling:      true,  // Use sync.Pool to reduce GC pressure
		},
		Files: map[string]FileConfig{
			// 'index' is the centralized logger.
			// It's configured to write to console (ConsoleStd) but not to a file (UseFileWriter: false).
			"index": {
				Path:          "logs/Index/Index.log",
				MinLevel:      "debug", // Capture all levels, though 'info' is default
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     false,
				SlogWriter:    true,  // Use the structured slog writer
				ConsoleStd:    true,  // Output to console
				UseFileWriter: false, // Do not write to 'Index.log' by default
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			// 'debug' logger, writes to file, disabled by default (DevelopmentMode: false)
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
				ConsoleStd:    false, // Do not echo debug logs to console
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			// 'event' logger, uses standard text writer
			"event": {
				Path:          "logs/event/Event.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false, // Use standard log format
				StdWriter:     true,  // Use the standard log.Logger
				SlogWriter:    false, // Do not use slog.Logger
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			// 'message' logger, uses standard text writer
			"message": {
				Path:          "logs/Message/Message.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true, // Note: This is true, but SlogWriter is false
				StdWriter:     true, // Will use standard logger
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			// 'error' logger, writes to file
			// Also forwards logs to 'event' and 'message' loggers
			"error": {
				Path:          "logs/error/Error.log",
				MinLevel:      "debug", // Capture all error levels
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
					StdOutputs: []OutputTarget{ // Forward to these loggers
						{Type: "event"},
						{Type: "message"},
					},
				},
			},
			// 'critical' logger, writes to file and console
			// Also forwards logs to 'message' and 'error' loggers
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
				ConsoleStd:    true, // Echo critical errors to console
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs: []OutputTarget{ // Forward to these loggers
						{Type: "message"},
						{Type: "error"},
					},
				},
			},
			// 'http' logger for web requests
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
			// 'nmm' (Network Message Manager?) logger
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

// TestLogConfiguration returns a minimal configuration for testing.
// It disables file writing and centralized logging to avoid side effects during tests.
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
			EnableCentralizedLogging: false, // Disabled for tests
			LogChannel:               10000,
			WorkerPoolSize:           4,
			MaxWorkerPoolSize:        16,
			EnableObjectPooling:      true,
		},
		Files: map[string]FileConfig{
			// Index logger configured to discard everything
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
				UseFileWriter: false, // Disabled for tests
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
		},
	}
}

// LoadConfigurationFromFile loads a logging configuration from a JSON file.
// It atomically updates the global 'Config' variable, making the change
// thread-safe and visible to all logger functions.
func LoadConfigurationFromFile(filePath string) error {
	file, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read configuration file: %w", err)
	}

	var loadedConfig LogConfiguration

	if err := json.Unmarshal(file, &loadedConfig); err != nil {
		return fmt.Errorf("failed to unmarshal configuration JSON: %w", err)
	}

	// Store the new config atomically.
	// Functions like getConfigValue() will now see this new version.
	Config.Store(&loadedConfig)
	return nil
}

// LoadConfigurationFromBytes loads a logging configuration from a byte slice.
// It atomically updates the global 'Config' variable, making the change
// thread-safe and visible to all logger functions.
func LoadConfigurationFromBytes(data []byte) error {
	var loadedConfig LogConfiguration

	if err := json.Unmarshal(data, &loadedConfig); err != nil {
		return fmt.Errorf("failed to unmarshal configuration JSON from bytes: %w", err)
	}

	// Store the new config atomically.
	// Functions like getConfigValue() will now see this new version.
	Config.Store(&loadedConfig)
	return nil
}

// LoadConfigurationFromStruct loads a logging configuration from an existing struct.
// It atomically updates the global 'Config' variable, making the change
// thread-safe and visible to all logger functions.
func LoadConfigurationFromStruct(config *LogConfiguration) error {
	if config == nil {
		return fmt.Errorf("cannot load a nil LogConfiguration struct")
	}

	// Store the provided config struct atomically.
	// Functions like getConfigValue() will now see this new version.
	Config.Store(config)
	return nil
}

// GetLogLevel retrieves the current minimum log level for a specific logger type.
// It safely accesses the AppLogger map to find the correct logger.
func GetLogLevel(logType string) (LogLevel, error) {
	if !isLoggerActive() {
		return LevelInfo, fmt.Errorf("logging system not initialized")
	}

	// Use RLock for safe concurrent read access to AppLogger
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

	return targetLogger.level, nil
}

// getConfigValue safely retrieves the current global configuration.
// It loads the value from the atomic.Value, ensuring thread safety.
func getConfigValue() *LogConfiguration {
	val := Config.Load()
	if val == nil {
		return nil
	}
	// Cast the loaded interface{} back to *LogConfiguration
	return val.(*LogConfiguration)
}
