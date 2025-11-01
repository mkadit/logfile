package logfile

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"
)

// dispatchLog handles the logic of whether to log synchronously or asynchronously.
func dispatchLog(payload LogPayload, async bool) {
	if async {
		// Asynchronous: try to send to the channel without blocking.
		select {
		case logChannel <- payload:
			// Log was successfully queued.
		default:
			// Channel is full, log is dropped.
			log.Println("WARNING: Log channel is full. Log message dropped.")
		}
	} else {
		// Synchronous: call the logging function directly and block until it completes.
		logToSpecificLogger(
			payload.Logger, payload.Level, payload.EventType, payload.Err,
			payload.Msg, payload.TimeNow, payload.Ml, payload.OtherAttrs,
		)
	}
}

// Error logs an error event to configured loggers.
func Error(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if Testing || AppLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := LogPayload{
		Level:      LevelError,
		EventType:  "error",
		Err:        err,
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}
	if AppLogger.ErrorLogger != nil {
		payload.Logger = AppLogger.ErrorLogger
		dispatchLog(payload, async)
	}
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, async)
	}
}

// Info logs an info message to the message logger.
func Info(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || AppLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := LogPayload{
		Level:      LevelInfo,
		EventType:  "info",
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

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

// Warn logs a warning message to the message logger.
func Warn(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || AppLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := LogPayload{
		Level:      LevelWarn,
		EventType:  "warn",
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

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

// Debug logs a debug message to the debug logger.
func Debug(ml *MessageLog, async bool, message string, attr ...any) {
	currentConfig := getConfigValue()
	if Testing || AppLogger == nil || AppLogger.DebugLogger == nil || !currentConfig.General.DevelopmentMode {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := LogPayload{
		Level:      LevelDebug,
		EventType:  "debug",
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

	if AppLogger.EventLogger != nil {
		payload.Logger = AppLogger.EventLogger
		dispatchLog(payload, async)
	}
	if AppLogger.DebugLogger != nil {
		payload.Logger = AppLogger.DebugLogger
		dispatchLog(payload, async)
	}
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		payload.EventType = "index"
		dispatchLog(payload, async)
	}
}

// HTTP logs an HTTP event to the http logger.
func HTTP(ml *MessageLog, async bool, message string, attr ...any) {
	if Testing || AppLogger == nil || AppLogger.HTTPLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := LogPayload{
		Level:      LevelInfo,
		EventType:  "http",
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

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

// Critical logs a critical error message.
func Critical(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if Testing || AppLogger == nil || AppLogger.CriticalLogger == nil {
		return
	}
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	payload := LogPayload{
		Level:      LevelCritical,
		EventType:  "critical",
		Err:        err,
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

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

// Fatal logs a fatal error and exits. This must always be synchronous.
func Fatal(ml *MessageLog, err error, message string, attr ...any) {
	if Testing {
		log.Fatalf("FATAL: %s %v", fmt.Sprintf(message, attr...), err)
		return // Exit early in testing
	}

	payload := LogPayload{
		Level:      LevelCritical,
		EventType:  "fatal",
		Err:        err,
		Msg:        message,
		TimeNow:    time.Now().Format(DefaultTimeFormat),
		Ml:         ml,
		OtherAttrs: attr,
	}

	// Log synchronously to ensure messages are written before exit.
	if AppLogger != nil && AppLogger.CriticalLogger != nil {
		payload.Logger = AppLogger.CriticalLogger
		dispatchLog(payload, false) // ALWAYS SYNC
	}
	currentConfig := getConfigValue()
	if AppLogger != nil && currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		payload.Logger = AppLogger.IndexLogger
		dispatchLog(payload, false) // ALWAYS SYNC
	}

	// Ensure all buffered async logs are flushed before exiting.
	Shutdown()

	os.Exit(1)
}

// OperationStart creates a MessageLog for operation tracking and logs the start
func OperationStart(action, reffTrx, entity, typeTrx, url string) *MessageLog {
	ml := CreateMessageLog(action, reffTrx, entity, typeTrx, url)
	Info(ml, true, "Operation started",
		slog.String("operation", "start"),
		slog.String("operation_type", action))
	return ml
}

// OperationStep logs an operation step with automatic timing and step advancement
func OperationStep(ml *MessageLog, async bool, stepName, message string, attr ...any) {
	if ml != nil {
		// Record current step duration and advance to next step
		ml.RecordStepDuration()

		// Add step information to attributes
		stepAttrs := []any{
			slog.String("step_name", stepName),
			slog.Int("step_number", ml.Step),
		}
		stepAttrs = append(stepAttrs, attr...)

		Info(ml, async, message, stepAttrs...)
	} else {
		// Fallback if no MessageLog provided
		Info(nil, async, message, attr...)
	}
}

// OperationComplete logs operation completion with comprehensive duration summary
func OperationComplete(ml *MessageLog, async bool, message string, attr ...any) {
	if ml != nil {
		// Record final step duration
		ml.RecordStepDuration()

		totalDuration := ml.GetDurationSinceStart()
		durationSummary := ml.GetDurationSummary()

		completeAttrs := []any{
			slog.String("operation", "complete"),
			slog.String("total_duration", totalDuration.String()),
			slog.Int("total_steps", ml.Step),
			slog.Any("duration_summary", durationSummary),
		}
		completeAttrs = append(completeAttrs, attr...)

		Info(ml, async, message, completeAttrs...)
	} else {
		Info(nil, async, message, attr...)
	}
}

// OperationError logs an error during operation with timing context
func OperationError(ml *MessageLog, async bool, err error, message string, attr ...any) {
	if ml != nil {
		totalDuration := ml.GetDurationSinceStart()
		stepDuration := ml.GetDurationSinceLastLog()

		errorAttrs := []any{
			slog.String("operation", "error"),
			slog.String("total_duration", totalDuration.String()),
			slog.String("step_duration", stepDuration.String()),
			slog.Int("failed_at_step", ml.Step),
		}
		errorAttrs = append(errorAttrs, attr...)

		Error(ml, async, err, message, errorAttrs...)
	} else {
		Error(nil, async, err, message, attr...)
	}
}

// PerformanceMetric logs performance-related metrics
func PerformanceMetric(ml *MessageLog, async bool, metricName string, duration time.Duration, attr ...any) {
	perfAttrs := []any{
		slog.String("metric_name", metricName),
		slog.String("metric_duration", duration.String()),
		slog.String("metric_type", "performance"),
	}

	if ml != nil {
		perfAttrs = append(perfAttrs,
			slog.String("context_total_duration", ml.GetDurationSinceStart().String()),
			slog.Int("context_step", ml.Step))
	}

	perfAttrs = append(perfAttrs, attr...)
	Info(ml, async, "Performance metric recorded", perfAttrs...)
}

// SlowOperation logs when an operation exceeds expected duration
func SlowOperation(ml *MessageLog, async bool, expectedDuration, actualDuration time.Duration, message string, attr ...any) {
	slowAttrs := []any{
		slog.String("performance_alert", "slow_operation"),
		slog.String("expected_duration", expectedDuration.String()),
		slog.String("actual_duration", actualDuration.String()),
		slog.Float64("slowness_ratio", actualDuration.Seconds()/expectedDuration.Seconds()),
	}
	slowAttrs = append(slowAttrs, attr...)

	Warn(ml, async, message, slowAttrs...)
}
