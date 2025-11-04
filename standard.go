// logfile/standard.go - Standard logging functions
// This file contains the main public API functions (Info, Error, etc.)
// that applications use to write logs.
package logfile

import (
	"fmt"      // For string formatting
	"log"      // Standard Go logging
	"log/slog" // Structured logging
	"os"       // For os.Exit in Fatal
	"time"     // For timestamps
)

// dispatchLog routes a LogPayload to either the async channel or the sync logger.
// This is the central function for deciding how a log is processed.
func dispatchLog(payload LogPayload, async bool) {
	if async {
		// Asynchronous mode: Try to send to the log channel.
		select {
		case logChannel <- payload:
			// Successfully queued the log message.
		default:
			// Channel is full. Log would block.
			// Drop the message and log a warning to stderr.
			log.Println("WARNING: Log channel is full. Log message dropped.")
			// Since the payload was not sent, we must return it to the pool
			// if pooling is enabled (assuming it was retrieved from pool).
			if Config != nil && Config.General.EnableObjectPooling {
				putLogPayload(&payload)
			}
		}
	} else {
		// Synchronous mode: Write the log immediately in the current goroutine.
		logToSpecificLogger(
			payload.Logger, payload.Level, payload.EventType, payload.Err,
			payload.Msg, payload.TimeNow, payload.Ml, payload.OtherAttrs,
		)
		// Return the payload to the pool after synchronous write.
		if Config != nil && Config.General.EnableObjectPooling {
			putLogPayload(&payload)
		}
	}
}

// getPayload (internal helper) retrieves a LogPayload from the pool,
// populating it with common data.
func getPayload(level LogLevel, eventType string, err error, msg string, ml *MessageLog, attr []any) LogPayload {
	// Retrieve from pool if enabled, otherwise create new.
	var payload *LogPayload
	if Config != nil && Config.General.EnableObjectPooling {
		payload = getLogPayload()
	} else {
		payload = &LogPayload{}
	}

	payload.Level = level
	payload.EventType = eventType
	payload.Err = err
	payload.Msg = msg
	payload.TimeNow = time.Now().Format(DefaultTimeFormat)
	payload.Ml = ml
	// Note: We assign the slice directly. `dispatchLog` or its callee
	// `logToSpecificLogger` is responsible for handling it.
	// If pooling, `OtherAttrs` should be copied if the slice is reused.
	// *Correction*: The current implementation passes the `attr` slice directly.
	// `putLogPayload` will clear `OtherAttrs`, which is fine as it's
	// the `LogPayload` being pooled, not the `attr` slice itself.
	payload.OtherAttrs = attr
	return *payload
}

// Error logs an error event.
// It logs to EventLogger, ErrorLogger, and optionally IndexLogger.
func Error(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if Testing || AppLogger == nil {
		return
	}

	loggerMutex.RLock() // Read lock to safely access AppLogger.
	defer loggerMutex.RUnlock()

	// Base payload for error.
	payload := getPayload(LevelError, "error", err, message, ml, attr)

	// Log to EventLogger if configured.
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Log to ErrorLogger if configured.
	if AppLogger.ErrorLogger != nil {
		payload.Logger = AppLogger.ErrorLogger
		dispatchLog(payload, async)
	}

	// If centralized logging is enabled, also log to IndexLogger.
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, async)
	}
}

// Info logs an informational message.
// It logs to EventLogger and optionally IndexLogger.
func Info(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || AppLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := getPayload(LevelInfo, "info", nil, message, ml, attr)

	// Log to EventLogger.
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// If centralized logging enabled, also send to IndexLogger.
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index" // Change event type for index.
		dispatchLog(payload, async)
	}
}

// Warn logs a warning message.
// It logs to EventLogger and optionally IndexLogger.
func Warn(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || AppLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := getPayload(LevelWarn, "warn", nil, message, ml, attr)

	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// Debug logs detailed debugging information.
// It only logs if DevelopmentMode is enabled in the configuration.
func Debug(ml *MessageLog, async bool, message string, attr ...any) {
	currentConfig := getConfigValue()

	// Skip if testing, logger not initialized, no debug logger, or not in dev mode.
	if Testing || AppLogger == nil || AppLogger.DebugLogger == nil || !currentConfig.General.DevelopmentMode {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := getPayload(LevelDebug, "debug", nil, message, ml, attr)

	// Log to EventLogger.
	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}

	// Log to DebugLogger.
	if AppLogger.DebugLogger != nil {
		payload.Logger = AppLogger.DebugLogger
		dispatchLog(payload, async)
	}

	// Also send to IndexLogger if centralized logging enabled.
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// HTTP logs HTTP-related events (e.g., requests, responses).
func HTTP(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || AppLogger == nil || AppLogger.HTTPLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := getPayload(LevelInfo, "http", nil, message, ml, attr)

	if AppLogger.HTTPLogger != nil {
		payload.Logger = AppLogger.HTTPLogger
		dispatchLog(payload, async)
	}

	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// Critical logs a critical error that requires immediate attention.
func Critical(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if Testing || AppLogger == nil || AppLogger.CriticalLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := getPayload(LevelCritical, "critical", err, message, ml, attr)

	if AppLogger.CriticalLogger != nil {
		payload.Logger = AppLogger.CriticalLogger
		dispatchLog(payload, async)
	}

	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// Fatal logs a fatal error and terminates the application with os.Exit(1).
// This function *always* logs synchronously to ensure the message is written.
func Fatal(ml *MessageLog, err error, message string, attr ...any) {
	// In testing mode, use standard log.Fatalf to stop the test.
	if Testing {
		log.Fatalf("FATAL: %s %v", fmt.Sprintf(message, attr...), err)
		return
	}

	payload := getPayload(LevelCritical, "fatal", err, message, ml, attr)

	// ALWAYS log synchronously (async=false).
	if AppLogger != nil && AppLogger.CriticalLogger != nil {
		payload.Logger = AppLogger.CriticalLogger
		dispatchLog(payload, false)
	}

	currentConfig := getConfigValue()
	if AppLogger != nil && currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, false)
	}

	// Flush all buffered logs before exiting.
	Shutdown()

	// Terminate the application.
	os.Exit(1)
}

// OperationStart creates a new MessageLog and logs the start of an operation.
// This is the entry point for tracking a multi-step workflow.
func OperationStart(action, reffTrx, entity, typeTrx, url string) *MessageLog {
	// Create the MessageLog to track this operation.
	ml := CreateMessageLog(action, reffTrx, entity, typeTrx, url)

	// Log the start event.
	Info(ml, true, "Operation started",
		slog.String("operation", "start"),
		slog.String("operation_type", action))

	return ml
}

// OperationStep logs a step within an ongoing operation.
// It automatically records the timing of the *previous* step and advances
// the MessageLog to the *next* step.
func OperationStep(ml *MessageLog, async bool, stepName, message string, attr ...any) {
	if ml != nil {
		// Record how long the previous step took and advance to the next step.
		ml.RecordStepDuration()

		// Add step information to the log attributes.
		stepAttrs := []any{
			slog.String("step_name", stepName),
			slog.Int("step_number", ml.Step), // ml.Step is now the *new* step number.
		}
		stepAttrs = append(stepAttrs, attr...)

		// Log the step.
		Info(ml, async, message, stepAttrs...)
	} else {
		// Fallback if no MessageLog provided.
		Info(nil, async, message, attr...)
	}
}

// OperationComplete logs the successful completion of an operation.
// It records the duration of the final step and logs a summary.
func OperationComplete(ml *MessageLog, async bool, message string, attr ...any) {
	if ml != nil {
		// Record the duration of the final step.
		ml.RecordStepDuration()

		totalDuration := ml.GetDurationSinceStart()
		durationSummary := ml.GetDurationSummary() // Get summary map.

		// Build attributes with completion information.
		completeAttrs := []any{
			slog.String("operation", "complete"),
			slog.String("total_duration", totalDuration.String()),
			slog.Int("total_steps", ml.Step), // Step counter was advanced by RecordStepDuration
			slog.Any("duration_summary", durationSummary),
		}
		completeAttrs = append(completeAttrs, attr...)

		Info(ml, async, message, completeAttrs...)
	} else {
		Info(nil, async, message, attr...)
	}
}

// OperationError logs an error that occurred during an operation.
// It includes timing context up to the point of failure.
func OperationError(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if ml != nil {
		// Get timing information up to the point of failure.
		totalDuration := ml.GetDurationSinceStart()
		stepDuration := ml.GetDurationSinceLastLog() // Duration of the failing step.

		errorAttrs := []any{
			slog.String("operation", "error"),
			slog.String("total_duration", totalDuration.String()),
			slog.String("step_duration", stepDuration.String()),
			slog.Int("failed_at_step", ml.Step), // The step number *during* which it failed.
		}
		errorAttrs = append(errorAttrs, attr...)

		Error(ml, async, err, message, errorAttrs...)
	} else {
		Error(nil, async, err, message, attr...)
	}
}

// PerformanceMetric logs a specific performance measurement (e.g., a DB query).
func PerformanceMetric(ml *MessageLog, async bool, metricName string, duration time.Duration, attr ...any) {
	// Build attributes with metric information.
	perfAttrs := []any{
		slog.String("metric_name", metricName),
		slog.String("metric_duration", duration.String()),
		slog.String("metric_type", "performance"),
	}

	// If we have operation context, add it.
	if ml != nil {
		perfAttrs = append(perfAttrs,
			slog.String("context_total_duration", ml.GetDurationSinceStart().String()),
			slog.Int("context_step", ml.Step))
	}

	perfAttrs = append(perfAttrs, attr...)
	Info(ml, async, "Performance metric recorded", perfAttrs...)
}

// SlowOperation logs a warning when an operation exceeds an expected duration.
func SlowOperation(ml *MessageLog, async bool, expectedDuration, actualDuration time.Duration, message string, attr ...any) {
	// Calculate how much slower than expected.
	slownessRatio := 0.0
	if expectedDuration > 0 {
		slownessRatio = actualDuration.Seconds() / expectedDuration.Seconds()
	}

	slowAttrs := []any{
		slog.String("performance_alert", "slow_operation"),
		slog.String("expected_duration", expectedDuration.String()),
		slog.String("actual_duration", actualDuration.String()),
		slog.Float64("slowness_ratio", slownessRatio), // e.g., 2.5 means 2.5x slower
	}
	slowAttrs = append(slowAttrs, attr...)

	// Log as a warning.
	Warn(ml, async, message, slowAttrs...)
}
