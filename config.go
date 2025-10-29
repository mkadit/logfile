// logfile/config.go - Configuration loading and management
package logfile

import (
	"encoding/json"
	"fmt"
	"os"
)

var (
	LogAsync   = false
	SystemName = "APPLICATION"
)

// Table legend for mapping
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

// Updated DefaultLogConfiguration function for config.go
func DefaultLogConfiguration() LogConfiguration {
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
			EnableCentralizedLogging: true,
			LogChannel:               10000,
		},
		Files: map[string]FileConfig{
			"index": {
				Path:          "logs/Index/Index.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     false,
				SlogWriter:    true,
				ConsoleStd:    true,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
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
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			"event": {
				Path:          "logs/event/Event.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     true,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
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
			"error": {
				Path:          "logs/error/Error.log",
				MinLevel:      "debug",
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
					SlogOutputs: []OutputTarget{}, // 'error' type itself doesn't produce slog output
					StdOutputs: []OutputTarget{ // Redirect 'error's STD output to 'event' and 'message'
						{Type: "event"},
						{Type: "message"},
					},
				},
			},
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
				ConsoleStd:    true,
				UseFileWriter: true,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs: []OutputTarget{
						{Type: "message"},
						{Type: "error"},
					},
				},
			},
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
			EnableCentralizedLogging: false,
			LogChannel:               10000,
		},
		Files: map[string]FileConfig{
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
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			"debug": {
				Path:          "logs/Debug/Debug.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			"event": {
				Path:          "logs/event/Event.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			"message": {
				Path:          "logs/Message/Message.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			"error": {
				Path:          "logs/error/Error.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{}, // 'error' type itself doesn't produce slog output
					StdOutputs: []OutputTarget{ // Redirect 'error's STD output to 'event' and 'message'
						{Type: "event"},
						{Type: "message"},
					},
				},
			},
			"critical": {
				Path:          "logs/critical/Critical.log",
				MinLevel:      "debug",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs: []OutputTarget{
						{Type: "message"},
						{Type: "error"},
					},
				},
			},
			"http": {
				Path:          "logs/http/HTTP.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
			"nmm": {
				Path:          "logs/NMM/NMM.log",
				MinLevel:      "info",
				MaxSizeMB:     80,
				MaxBackups:    5,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    false,
				StdWriter:     false,
				SlogWriter:    false,
				ConsoleStd:    false,
				UseFileWriter: false,
				AdditionalOutputs: AdditionalOutputConfig{
					SlogOutputs: []OutputTarget{},
					StdOutputs:  []OutputTarget{},
				},
			},
		},
	}
}

// LoadConfigurationFromFile loads the logging configuration from a JSON file.
func LoadConfigurationFromFile(filePath string) error {
	file, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read configuration file: %w", err)
	}

	var loadedConfig LogConfiguration
	if err := json.Unmarshal(file, &loadedConfig); err != nil {
		return fmt.Errorf("failed to unmarshal configuration JSON: %w", err)
	}

	loggerMutex.Lock()
	defer loggerMutex.Unlock()
	Config = &loadedConfig
	return nil
}

// GetLogLevel returns the minimum log level for a given log type.
func GetLogLevel(logType string) (LogLevel, error) {
	if AppLogger == nil { // Use the global AppLogger from types.go
		return LevelInfo, fmt.Errorf("logging system not initialized; cannot get log level")
	}

	loggerMutex.RLock() // Use the global loggerMutex from types.go
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

func getConfigValue() *LogConfiguration {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return Config
}
