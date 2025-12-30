// logfile/standard.go - Standard logging functions
// FIXED: Proper MessageLog handling and channel safety
package logfile

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"
)

// dispatchLog routes a LogPayload to either the async channel or the sync logger.
// This is the central dispatcher for all log messages.
func dispatchLog(payload LogPayload, async bool) {
	// Check the 'async' flag passed by the user (e.g., Info(ml, true, ...))
	if async {
		// Get channel safely
		ch, ok := getLogChannelSafe()
		if !ok {
			// System is shutting down or not initialized.
			log.Println("WARNING: Logging system not available. Log message dropped.")
			// Return payload to pool if enabled
			currentConfig := getConfigValue()
			if currentConfig != nil && currentConfig.General.EnableObjectPooling {
				putLogPayload(&payload)
			}
			return
		}

		// Try to send to channel in a non-blocking way
		select {
		case ch <- payload:
			// Successfully queued
		default:
			// Channel is full, message is dropped.
			log.Println("WARNING: Log channel is full. Log message dropped.")
			// Return payload to pool if enabled
			currentConfig := getConfigValue()
			if currentConfig != nil && currentConfig.General.EnableObjectPooling {
				putLogPayload(&payload)
			}
		}
	} else {
		// Synchronous mode: Write immediately in the current goroutine.
		logToSpecificLogger(
			payload.Logger, payload.Level, payload.EventType, payload.Err,
			payload.Msg, payload.TimeNow, payload.Ml, payload.OtherAttrs,
		)
		// Return payload to pool if enabled
		currentConfig := getConfigValue()
		if currentConfig != nil && currentConfig.General.EnableObjectPooling {
			putLogPayload(&payload)
		}
	}
}

// getPayload retrieves a LogPayload from the pool and populates it.
// It handles cloning the MessageLog and deep-copying attributes
// if the log is asynchronous, to prevent data races.
func getPayload(level LogLevel, eventType string, err error, msg string, ml *MessageLog, attr []any, async bool) LogPayload {
	var payload *LogPayload
	currentConfig := getConfigValue()

	if currentConfig != nil && currentConfig.General.EnableObjectPooling {
		payload = getLogPayload() // Get from pool
	} else {
		payload = &LogPayload{} // Allocate new
	}

	payload.Level = level
	payload.EventType = eventType
	payload.Err = err
	payload.Msg = msg
	payload.TimeNow = time.Now().Format(DefaultTimeFormat)

	// CRITICAL FIX: If logging asynchronously, we *must* clone the MessageLog.
	// Otherwise, the logger worker goroutine will read it at the same time
	// the main goroutine is modifying it (e.g., in OperationStep).
	if async && ml != nil {
		payload.Ml = ml.Clone()
	} else {
		// If sync, we can just pass the pointer.
		payload.Ml = ml
	}

	// Deep copy attributes for async logging to prevent data races
	// if the caller modifies the underlying slice/map after logging.
	if async && len(attr) > 0 {
		payload.OtherAttrs = deepCopyAttrs(attr)
	} else {
		payload.OtherAttrs = attr
	}

	return *payload
}

// deepCopyAttrs creates a deep copy of attributes for async logging.
// This is a defensive copy to prevent data races.
func deepCopyAttrs(attr []any) []any {
	if len(attr) == 0 {
		return nil
	}

	copied := make([]any, len(attr))
	for i, a := range attr {
		if sa, ok := a.(slog.Attr); ok {
			// Deep copy the attribute value
			copied[i] = slog.Attr{
				Key: sa.Key,
				// slog.AnyValue handles the value encapsulation
				Value: slog.AnyValue(deepCopyAny(sa.Value.Any())),
			}
		} else {
			// Deep copy other 'any' types
			copied[i] = deepCopyAny(a)
		}
	}
	return copied
}

// Error logs an error event.
// It writes to EventLogger, ErrorLogger, and (if enabled) IndexLogger.
func Error(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if Testing || !isLoggerActive() {
		return
	}

	loggerMutex.RLock() // Read lock to safely access AppLogger
	defer loggerMutex.RUnlock()

	if AppLogger == nil {
		return
	}

	// Get a payload (clones/copies if async)
	payload := getPayload(LevelError, "error", err, message, ml, attr, async)

	// Dispatch to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Dispatch to ErrorLogger
	if AppLogger.ErrorLogger != nil {
		payload.Logger = AppLogger.ErrorLogger
		dispatchLog(payload, async)
	}

	// Dispatch to centralized IndexLogger
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, async)
	}
}

// Info logs an informational message.
// It writes to EventLogger and (if enabled) IndexLogger.
func Info(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || !isLoggerActive() {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil {
		return
	}

	payload := getPayload(LevelInfo, "info", nil, message, ml, attr, async)

	// Dispatch to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Dispatch to centralized IndexLogger
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index" // Mark as 'index' type for this logger
		dispatchLog(payload, async)
	}
}

// Warn logs a warning message.
// It writes to EventLogger and (if enabled) IndexLogger.
func Warn(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || !isLoggerActive() {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil {
		return
	}

	payload := getPayload(LevelWarn, "warn", nil, message, ml, attr, async)

	// Dispatch to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Dispatch to centralized IndexLogger
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// Debug logs detailed debugging information.
// It only logs if DevelopmentMode is true.
// It writes to EventLogger, DebugLogger, and (if enabled) IndexLogger.
func Debug(ml *MessageLog, async bool, message string, attr ...any) {
	currentConfig := getConfigValue()

	// Exit early if not in development mode
	if Testing || !isLoggerActive() || currentConfig == nil || !currentConfig.General.DevelopmentMode {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil || AppLogger.DebugLogger == nil {
		return
	}

	payload := getPayload(LevelDebug, "debug", nil, message, ml, attr, async)

	// Dispatch to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Dispatch to DebugLogger
	if AppLogger.DebugLogger != nil {
		payload.Logger = AppLogger.DebugLogger
		dispatchLog(payload, async)
	}

	// Dispatch to centralized IndexLogger
	if currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// HTTP logs HTTP-related events.
// It writes to HTTPLogger and (if enabled) IndexLogger.
func HTTP(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || !isLoggerActive() {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil || AppLogger.HTTPLogger == nil {
		return
	}

	payload := getPayload(LevelInfo, "http", nil, message, ml, attr, async)

	// Dispatch to HTTPLogger
	if AppLogger.HTTPLogger != nil {
		payload.Logger = AppLogger.HTTPLogger
		dispatchLog(payload, async)
	}

	// Dispatch to centralized IndexLogger
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// Critical logs a critical error.
// It writes to CriticalLogger and (if enabled) IndexLogger.
func Critical(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if Testing || !isLoggerActive() {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil || AppLogger.CriticalLogger == nil {
		return
	}

	payload := getPayload(LevelCritical, "critical", err, message, ml, attr, async)

	// Dispatch to CriticalLogger
	if AppLogger.CriticalLogger != nil {
		payload.Logger = AppLogger.CriticalLogger
		dispatchLog(payload, async)
	}

	// Dispatch to centralized IndexLogger
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// Fatal logs a fatal error and terminates the application.
// This function ALWAYS logs synchronously to ensure messages are written.
// It writes to CriticalLogger, IndexLogger, then calls Shutdown() and os.Exit(1).
func Fatal(ml *MessageLog, err error, message string, attr ...any) {
	if Testing {
		// In tests, just log to stderr and return to allow test to fail
		log.Fatalf("FATAL: %s %v", fmt.Sprintf(message, attr...), err)
		return
	}

	// ALWAYS log synchronously for Fatal
	payload := getPayload(LevelCritical, "fatal", err, message, ml, attr, false)

	loggerMutex.RLock()
	// Dispatch to CriticalLogger
	if AppLogger != nil && AppLogger.CriticalLogger != nil {
		payload.Logger = AppLogger.CriticalLogger
		dispatchLog(payload, false) // false = synchronous
	}

	// Dispatch to centralized IndexLogger
	currentConfig := getConfigValue()
	if AppLogger != nil && currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, false) // false = synchronous
	}
	loggerMutex.RUnlock()

	// Flush all buffered logs before exiting
	Shutdown()

	// Terminate the application
	os.Exit(1)
}

// OperationStart creates a new MessageLog and logs the start of an operation.
// This is a helper function to standardize operation logging.
func OperationStart(action, reffTrx, entity, typeTrx, url string) *MessageLog {
	// Create a new context for this operation
	ml := CreateMessageLog(action, reffTrx, entity, typeTrx, url)

	// Log the start event
	Info(ml, true, "Operation started", // true = async
		slog.String("operation", "start"),
		slog.String("operation_type", action))

	return ml
}

// OperationStep logs a step within an ongoing operation.
// It modifies the MessageLog by recording the step duration.
// IMPORTANT: This modifies ml.Step, so don't use the same ml concurrently
// without cloning. The 'async' flag handles cloning in getPayload.
func OperationStep(ml *MessageLog, async bool, stepName, message string, attr ...any) {
	if ml != nil {
		// Record the duration of the *previous* step
		ml.RecordStepDuration()

		// Prepare attributes for this step
		stepAttrs := []any{
			slog.String("step_name", stepName),
			slog.Int("step_number", ml.GetCurrentStep()),
		}
		stepAttrs = append(stepAttrs, attr...)

		Info(ml, async, message, stepAttrs...)
	} else {
		// Log without operation context
		Info(nil, async, message, attr...)
	}
}

// OperationComplete logs the successful completion of an operation.
// It records the final step duration and logs a summary.
func OperationComplete(ml *MessageLog, async bool, message string, attr ...any) {
	if ml != nil {
		// Record duration of the final step
		ml.RecordStepDuration()

		totalDuration := ml.GetDurationSinceStart()
		durationSummary := ml.GetDurationSummary() // Get map[int]duration

		// Prepare summary attributes
		completeAttrs := []any{
			slog.String("operation", "complete"),
			slog.String("total_duration", totalDuration.String()),
			slog.Int("total_steps", ml.GetCurrentStep()),
			slog.Any("duration_summary", durationSummary),
		}
		completeAttrs = append(completeAttrs, attr...)

		Info(ml, async, message, completeAttrs...)
	} else {
		Info(nil, async, message, attr...)
	}
}

// OperationError logs an error that occurred during an operation.
// It logs the total duration and at which step the error occurred.
func OperationError(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if ml != nil {
		totalDuration := ml.GetDurationSinceStart()
		// Get duration since the last step
		stepDuration := ml.GetDurationSinceLastLog()

		// Prepare error attributes
		errorAttrs := []any{
			slog.String("operation", "error"),
			slog.String("total_duration", totalDuration.String()),
			slog.String("step_duration", stepDuration.String()),
			slog.Int("failed_at_step", ml.GetCurrentStep()),
		}
		errorAttrs = append(errorAttrs, attr...)

		Error(ml, async, err, message, errorAttrs...)
	} else {
		Error(nil, async, err, message, attr...)
	}
}

// PerformanceMetric logs a specific performance measurement.
// This is for logging arbitrary durations that aren't part of a formal "step".
func PerformanceMetric(ml *MessageLog, async bool, metricName string, duration time.Duration, attr ...any) {
	perfAttrs := []any{
		slog.String("metric_name", metricName),
		slog.String("metric_duration", duration.String()),
		slog.String("metric_type", "performance"),
	}

	// Add operation context if available
	if ml != nil {
		perfAttrs = append(perfAttrs,
			slog.String("context_total_duration", ml.GetDurationSinceStart().String()),
			slog.Int("context_step", ml.GetCurrentStep()))
	}

	perfAttrs = append(perfAttrs, attr...)
	Info(ml, async, "Performance metric recorded", perfAttrs...)
}

// SlowOperation logs a warning when an operation exceeds an expected duration.
func SlowOperation(ml *MessageLog, async bool, expectedDuration, actualDuration time.Duration, message string, attr ...any) {
	slownessRatio := 0.0
	if expectedDuration > 0 {
		slownessRatio = actualDuration.Seconds() / expectedDuration.Seconds()
	}

	slowAttrs := []any{
		slog.String("performance_alert", "slow_operation"),
		slog.String("expected_duration", expectedDuration.String()),
		slog.String("actual_duration", actualDuration.String()),
		slog.Float64("slowness_ratio", slownessRatio),
	}
	slowAttrs = append(slowAttrs, attr...)

	Warn(ml, async, message, slowAttrs...)
}

// InfoCapture logs an informational message and returns the JSON representation.
// It writes to EventLogger and (if enabled) IndexLogger, just like Info().
func InfoCapture(ml *MessageLog, async bool, message string, attr ...any) (string, error) {
	if Testing || !isLoggerActive() {
		return "", fmt.Errorf("logging system not active or in test mode")
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil {
		return "", fmt.Errorf("no logger available")
	}

	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (clones/copies if async)
	payload := getPayload(LevelInfo, "info", nil, message, ml, attr, async)

	// Generate JSON from the payload
	jsonBytes, err := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if err != nil {
		return "", fmt.Errorf("failed to format JSON: %w", err)
	}

	// Log to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Log to centralized IndexLogger
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index" // Mark as 'index' type for this logger
		dispatchLog(payload, async)
	}

	return string(jsonBytes), nil
}

// ErrorCapture logs an error message and returns the JSON representation.
// It writes to EventLogger, ErrorLogger, and (if enabled) IndexLogger, just like Error().
func ErrorCapture(ml *MessageLog, async bool, err error, message string, attr ...any) (string, error) {
	if Testing || !isLoggerActive() {
		return "", fmt.Errorf("logging system not active or in test mode")
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil {
		return "", fmt.Errorf("no logger available")
	}

	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (clones/copies if async)
	payload := getPayload(LevelError, "error", err, message, ml, attr, async)

	// Generate JSON from the payload
	jsonBytes, jsonErr := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if jsonErr != nil {
		return "", fmt.Errorf("failed to format JSON: %w", jsonErr)
	}

	// Log to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Log to ErrorLogger
	if AppLogger.ErrorLogger != nil {
		payload.Logger = AppLogger.ErrorLogger
		dispatchLog(payload, async)
	}

	// Log to centralized IndexLogger
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, async)
	}

	return string(jsonBytes), nil
}

// WarnCapture logs a warning message and returns the JSON representation.
// It writes to EventLogger and (if enabled) IndexLogger, just like Warn().
func WarnCapture(ml *MessageLog, async bool, message string, attr ...any) (string, error) {
	if Testing || !isLoggerActive() {
		return "", fmt.Errorf("logging system not active or in test mode")
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil {
		return "", fmt.Errorf("no logger available")
	}

	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (clones/copies if async)
	payload := getPayload(LevelWarn, "warn", nil, message, ml, attr, async)

	// Generate JSON from the payload
	jsonBytes, err := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if err != nil {
		return "", fmt.Errorf("failed to format JSON: %w", err)
	}

	// Log to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Log to centralized IndexLogger
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}

	return string(jsonBytes), nil
}

// DebugCapture logs detailed debugging information and returns the JSON representation.
// It only logs if DevelopmentMode is true, just like Debug().
func DebugCapture(ml *MessageLog, async bool, message string, attr ...any) (string, error) {
	currentConfig := getConfigValue()

	// Exit early if not in development mode
	if Testing || !isLoggerActive() || currentConfig == nil || !currentConfig.General.DevelopmentMode {
		return "", fmt.Errorf("debug logging disabled or in test mode")
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil || AppLogger.DebugLogger == nil {
		return "", fmt.Errorf("no debug logger available")
	}

	// Create JSON formatter
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (clones/copies if async)
	payload := getPayload(LevelDebug, "debug", nil, message, ml, attr, async)

	// Generate JSON from the payload
	jsonBytes, err := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if err != nil {
		return "", fmt.Errorf("failed to format JSON: %w", err)
	}

	// Log to EventLogger
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Log to DebugLogger
	if AppLogger.DebugLogger != nil {
		payload.Logger = AppLogger.DebugLogger
		dispatchLog(payload, async)
	}

	// Log to centralized IndexLogger
	if currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}

	return string(jsonBytes), nil
}

// CriticalCapture logs a critical error and returns the JSON representation.
// It writes to CriticalLogger and (if enabled) IndexLogger, just like Critical().
func CriticalCapture(ml *MessageLog, async bool, err error, message string, attr ...any) (string, error) {
	if Testing || !isLoggerActive() {
		return "", fmt.Errorf("logging system not active or in test mode")
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger == nil || AppLogger.CriticalLogger == nil {
		return "", fmt.Errorf("no critical logger available")
	}

	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (clones/copies if async)
	payload := getPayload(LevelCritical, "critical", err, message, ml, attr, async)

	// Generate JSON from the payload
	jsonBytes, jsonErr := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if jsonErr != nil {
		return "", fmt.Errorf("failed to format JSON: %w", jsonErr)
	}

	// Log to CriticalLogger
	if AppLogger.CriticalLogger != nil {
		payload.Logger = AppLogger.CriticalLogger
		dispatchLog(payload, async)
	}

	// Log to centralized IndexLogger
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, async)
	}

	return string(jsonBytes), nil
}

// CaptureInfo generates JSON representation of an informational message without logging.
func CaptureInfo(ml *MessageLog, message string, attr ...any) (string, error) {
	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (no need to clone for capture-only)
	payload := getPayload(LevelInfo, "info", nil, message, ml, attr, false)

	// Generate JSON from the payload
	jsonBytes, err := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if err != nil {
		return "", fmt.Errorf("failed to format JSON: %w", err)
	}

	// Return payload to pool if enabled
	if currentConfig != nil && currentConfig.General.EnableObjectPooling {
		putLogPayload(&payload)
	}

	return string(jsonBytes), nil
}

// CaptureError generates JSON representation of an error message without logging.
func CaptureError(ml *MessageLog, err error, message string, attr ...any) (string, error) {
	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (no need to clone for capture-only)
	payload := getPayload(LevelError, "error", err, message, ml, attr, false)

	// Generate JSON from the payload
	jsonBytes, jsonErr := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if jsonErr != nil {
		return "", fmt.Errorf("failed to format JSON: %w", jsonErr)
	}

	// Return payload to pool if enabled
	if currentConfig != nil && currentConfig.General.EnableObjectPooling {
		putLogPayload(&payload)
	}

	return string(jsonBytes), nil
}

// CaptureWarn generates JSON representation of a warning message without logging.
func CaptureWarn(ml *MessageLog, message string, attr ...any) (string, error) {
	// Get configuration for JSON formatting
	currentConfig := getConfigValue()
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (no need to clone for capture-only)
	payload := getPayload(LevelWarn, "warn", nil, message, ml, attr, false)

	// Generate JSON from the payload
	jsonBytes, err := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if err != nil {
		return "", fmt.Errorf("failed to format JSON: %w", err)
	}

	// Return payload to pool if enabled
	if currentConfig != nil && currentConfig.General.EnableObjectPooling {
		putLogPayload(&payload)
	}

	return string(jsonBytes), nil
}

// CaptureDebug generates JSON representation of a debug message without logging.
// It only works if DevelopmentMode is true, just like Debug().
func CaptureDebug(ml *MessageLog, message string, attr ...any) (string, error) {
	currentConfig := getConfigValue()

	// Exit early if not in development mode
	if Testing || !currentConfig.General.DevelopmentMode {
		return "", fmt.Errorf("debug logging disabled or in test mode")
	}

	// Create JSON formatter
	jsonFormatter := NewJSONFormatter(
		currentConfig != nil && currentConfig.General.AddSource,
		DefaultTimeFormat,
	)

	// Create a payload (no need to clone for capture-only)
	payload := getPayload(LevelDebug, "debug", nil, message, ml, attr, false)

	// Generate JSON from the payload
	jsonBytes, err := jsonFormatter.FormatRecord(
		payload.Level,
		payload.EventType,
		payload.Err,
		payload.Msg,
		payload.TimeNow,
		payload.Ml,
		payload.OtherAttrs,
	)
	if err != nil {
		return "", fmt.Errorf("failed to format JSON: %w", err)
	}

	// Return payload to pool if enabled
	if currentConfig != nil && currentConfig.General.EnableObjectPooling {
		putLogPayload(&payload)
	}

	return string(jsonBytes), nil
}
