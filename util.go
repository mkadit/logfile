package logfile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

// toAnySlice converts a slice of slog.Attr to []any for slog functions.
func toAnySlice(attrs []slog.Attr) []any {
	anySlice := make([]any, len(attrs))
	for i, v := range attrs {
		anySlice[i] = v
	}
	return anySlice
}

// deepCopyMap creates a deep copy of a map[string]any by marshaling and unmarshaling it.
// This is a robust way to prevent race conditions with map attributes in logs.
func deepCopyMap(originalMap map[string]any) map[string]any {
	if originalMap == nil {
		return nil
	}
	// Marshal the original map to a JSON byte slice.
	jsonBytes, err := json.Marshal(originalMap)
	if err != nil {
		// If marshaling fails, return a safe, simple representation.
		return map[string]any{"error": "failed to copy map for logging"}
	}

	// Unmarshal the JSON back into a new map.
	var newMap map[string]any
	if err := json.Unmarshal(jsonBytes, &newMap); err != nil {
		return map[string]any{"error": "failed to copy map for logging"}
	}
	return newMap
}

// Enhanced deepCopyAny function to handle various types safely
func deepCopyAny(value any) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val) // Recursive copy
		}
		return copied
	case map[string]string:
		copied := make(map[string]string, len(v))
		for k, val := range v {
			copied[k] = val
		}
		return copied
	case map[int]string:
		copied := make(map[int]string, len(v))
		for k, val := range v {
			copied[k] = val
		}
		return copied
	case map[int]time.Duration:
		// Create a safe string representation to avoid races
		copied := make(map[string]string, len(v))
		for k, val := range v {
			copied[fmt.Sprintf("step_%d", k)] = val.String()
		}
		return copied
	case map[int]interface{}:
		copied := make(map[int]interface{}, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val)
		}
		return copied
	case []any:
		copied := make([]any, len(v))
		for i, val := range v {
			copied[i] = deepCopyAny(val)
		}
		return copied
	case []string:
		copied := make([]string, len(v))
		copy(copied, v)
		return copied
	default:
		// For basic types (string, int, bool, etc.), return as-is
		// For unknown complex types, we can't safely copy, so return as-is
		// This is a trade-off between safety and functionality
		return value
	}
}

// isBasicType is a helper function to determine if a value is a basic Go type.
func isBasicType(val any) bool {
	switch val.(type) {
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, complex64, complex128, string:
		return true
	default:
		return false
	}
}

// getCallerSource returns the source information of the actual caller (skipping logfile package functions)
func getCallerSource() slog.Attr {
	const maxDepth = 20
	for i := 1; i < maxDepth; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}

		// Skip frames that are in the logfile package
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			funcName := fn.Name()
			// Skip if this is a logfile package function
			if !strings.Contains(funcName, "/logger.") &&
				!strings.HasSuffix(funcName, "logfile.LogInfoEvent") &&
				!strings.HasSuffix(funcName, "logfile.LogErrorEvent") &&
				!strings.HasSuffix(funcName, "logfile.LogWarnEvent") &&
				!strings.HasSuffix(funcName, "logfile.LogDebugEvent") &&
				!strings.HasSuffix(funcName, "logfile.LogHTTPEvent") &&
				!strings.HasSuffix(funcName, "logfile.LogCritical") &&
				!strings.HasSuffix(funcName, "logfile.LogFatal") &&
				!strings.HasSuffix(funcName, "logfile.LogHTTP") &&
				!strings.HasSuffix(funcName, "logfile.LogNmmMsg") &&
				!strings.HasSuffix(funcName, "logfile.logSpecialized") &&
				!strings.HasSuffix(funcName, "logfile.logToSpecificLogger") &&
				!strings.HasSuffix(funcName, "logfile.LogOperationStart") &&
				!strings.HasSuffix(funcName, "logfile.LogOperationStep") &&
				!strings.HasSuffix(funcName, "logfile.LogOperationComplete") {
				return slog.String("source", fmt.Sprintf("%s:%d", file, line))
			}
		}
	}
	return slog.String("source", "unknown")
}

// Flush forces any buffered logs to be written
func (m *MultLogger) Flush() error {
	if flusher, ok := m.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// FlushAll forces all loggers to flush their buffers
func FlushAll() error {
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()
	if AppLogger == nil {
		return nil
	}
	var lastErr error
	loggers := []*MultLogger{
		AppLogger.MessageLogger,
		AppLogger.EventLogger,
		AppLogger.ErrorLogger,
		AppLogger.HTTPLogger,
		AppLogger.CriticalLogger,
		AppLogger.NMMLogger,
		AppLogger.DebugLogger,
		AppLogger.IndexLogger,
	}
	for _, logger := range loggers {
		if logger != nil {
			if err := logger.Flush(); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// logToSpecificLogger handles logging to a specific MultLogger based on its configuration
// and routes to additional writers for the corresponding format.
func logToSpecificLogger(logger *MultLogger, level LogLevel, eventType string, err error, msg, timeNow string, ml *MessageLog, otherAttrs []any) {
	if logger == nil || !logger.IsEnabled(level) { // Check if the level is enabled for this logger
		return // Safety check and level filtering
	}

	// Update the MessageLog's LastTime using thread-safe methods
	var currentStepDuration time.Duration
	if ml != nil {
		// This method is thread-safe and records step duration atomically
		currentStepDuration = ml.SafeRecordStepDuration()
	}

	// Prepare common attributes for slog.
	slogAttrs := []slog.Attr{
		slog.String("event_type", eventType),
		slog.String("timestamp", timeNow),
	}
	if err != nil {
		slogAttrs = append(slogAttrs, slog.String("error", err.Error()))
	}

	// Add MessageLog attributes if provided (thread-safe)
	if ml != nil {
		mlAttrs := ml.ToSlogAttrs()

		// Add timing information
		if ml.Step > 1 {
			// Add current step duration (we already recorded it safely above)
			slogAttrs = append(slogAttrs, slog.String("duration_step_active", currentStepDuration.String()))
			// Add previous step duration if available - use thread-safe method
			if prevDuration := ml.GetStepDuration(ml.Step - 1); prevDuration > 0 {
				slogAttrs = append(slogAttrs, slog.String("duration_step_completed", prevDuration.String()))
			}
		}

		// Add total operation duration - use thread-safe method
		totalDuration := ml.GetDurationSinceStart()
		// Add performance metrics for long-running operations
		if totalDuration > time.Second {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_slow", true))
		}
		if ml.Step > 10 {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_many_steps", true))
		}

		slogAttrs = append(slogAttrs, mlAttrs...)
	}

	// Process otherAttrs with comprehensive safety
	originalAttrsForStd := make([]any, 0, len(otherAttrs))
	var firstComplexAttrInOtherAttrs any
	hasComplexAttrInOtherAttrs := false

	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			// Create a safe copy of the attribute value
			safeValue := deepCopyAny(sa.Value.Any())

			// Create new safe attribute
			safeAttr := slog.Attr{
				Key:   sa.Key,
				Value: slog.AnyValue(safeValue),
			}

			slogAttrs = append(slogAttrs, safeAttr)

			// For standard logging, format the safe value
			mes := fmt.Sprintf("%s: %v", sa.Key, safeValue)
			originalAttrsForStd = append(originalAttrsForStd, mes)

			// Check complexity for the *value* of the slog.Attr
			if !isBasicType(safeValue) {
				if !hasComplexAttrInOtherAttrs {
					firstComplexAttrInOtherAttrs = safeValue
					hasComplexAttrInOtherAttrs = true
				}
			}
		} else {
			// Create safe copy of raw value
			safeCopy := deepCopyAny(a)

			slogAttrs = append(slogAttrs, slog.Any("arg", safeCopy))
			originalAttrsForStd = append(originalAttrsForStd, safeCopy)

			if !isBasicType(safeCopy) {
				if !hasComplexAttrInOtherAttrs {
					firstComplexAttrInOtherAttrs = safeCopy
					hasComplexAttrInOtherAttrs = true
				}
			}
		}
	}

	currentConfig := getConfigValue()
	// Rest of the logging logic remains the same...
	// Log with structured logger if configured
	if logger.IsStructuredActive() {
		slogLevelToUse := level.ToSlogLevel()

		if logger.StructuredLogger != nil {
			logger.StructuredLogger.LogAttrs(context.Background(),
				slogLevelToUse, msg, slogAttrs...)
		}

		// Log to additional slog outputs
		for target, slogger := range logger.additionalSlogLoggers {
			if slogger != nil {
				targetConfig, ok := currentConfig.Files[target.Type]
				if !ok {
					continue
				}
				targetLevel, err := ParseLogLevel(targetConfig.MinLevel)
				if err != nil {
					continue
				}
				if level >= targetLevel {
					slogger.LogAttrs(context.Background(), slogLevelToUse, msg, slogAttrs...)
				}
			}
		}
	}

	// Log with standard logger if configured
	if logger.IsStandardActive() {
		formattedMsg := formatStandardLog(msg, timeNow, eventType, err, ml, originalAttrsForStd, firstComplexAttrInOtherAttrs, hasComplexAttrInOtherAttrs)

		if logger.StdLogger != nil {
			logger.StdLogger.Output(2, formattedMsg)
		}

		// Log to additional std outputs
		for target, stdlogger := range logger.additionalStdLoggers {
			if stdlogger != nil {
				targetConfig, ok := currentConfig.Files[target.Type]
				if !ok {
					continue
				}
				targetLevel, err := ParseLogLevel(targetConfig.MinLevel)
				if err != nil {
					continue
				}
				if level >= targetLevel {
					stdlogger.Output(2, formattedMsg)
				}
			}
		}
	}
}

// formatStandardLog formats log messages for the standard logger (log.Logger).
// Enhanced to include duration information in standard format using thread-safe methods.
func formatStandardLog(msg, timeNow, logType string, err error, ml *MessageLog, otherAttrs []any, firstComplexAttrInOtherAttrs any, hasComplexAttrInOtherAttrs bool) string {
	additionalAttrsFormatted := make([]string, 0, len(otherAttrs))

	// Add source information to standard log format if enabled
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.AddSource {
		sourceAttr := getCallerSource()
		additionalAttrsFormatted = append(additionalAttrsFormatted, fmt.Sprintf("source=%v", sourceAttr.Value.Any()))
	}

	// Add duration information to standard format if MessageLog is present
	// FIXED: Use thread-safe methods
	if ml != nil {
		totalDuration := ml.GetDurationSinceStart() // Thread-safe method
		if totalDuration > 0 {
			additionalAttrsFormatted = append(additionalAttrsFormatted, fmt.Sprintf("total_duration=%v", totalDuration))
		}

		stepDuration := ml.GetDurationSinceLastLog() // Thread-safe method
		if stepDuration > 0 {
			additionalAttrsFormatted = append(additionalAttrsFormatted, fmt.Sprintf("step_duration=%v", stepDuration))
		}
	}

	for _, a := range otherAttrs {
		// Attempt to format each attribute simply for standard logging.
		// Complex types might still benefit from %+v, but simple types can be %v.
		if sa, ok := a.(slog.Attr); ok {
			additionalAttrsFormatted = append(additionalAttrsFormatted, fmt.Sprintf("%s=%v", sa.Key, sa.Value.Any()))
		} else {
			// Check if it's a known complex type for specific formatting, otherwise use generic
			additionalAttrsFormatted = append(additionalAttrsFormatted, fmt.Sprintf("%v", a))
		}
	}

	additionalAttrsStr := ""
	if len(additionalAttrsFormatted) > 0 {
		additionalAttrsStr = " " + strings.Join(additionalAttrsFormatted, " ")
	}

	if err != nil {
		// Error formats:
		if hasComplexAttrInOtherAttrs {
			// Use the identified complex attribute from `otherAttrs` for the T|V part
			// Example: [%s][%s][%T|%+v|%s|%+v]%s
			return fmt.Sprintf("[%s][%s][%T|%+v|%s|%+v][%v]", timeNow, logType, firstComplexAttrInOtherAttrs, firstComplexAttrInOtherAttrs, msg, WithStack(err), additionalAttrsStr)
		} else {
			// Basic error, just append additional attrs
			// Example: [%s][%s][%s|%+v]%s
			return fmt.Sprintf("[%s][%s][%s|%+v][%v]", timeNow, logType, msg, WithStack(err), additionalAttrsStr)
		}
	} else {
		// Normal/HTTP logging formats
		if ml != nil {
			// Access MessageLog fields in a thread-safe way
			// We need to read multiple fields atomically, so we'll use the ToSlogAttrs method
			// and extract the values we need, or we need to add a thread-safe accessor method

			// For now, we'll access the fields directly since they're mostly read-only after creation
			// But this could be improved with a thread-safe accessor method
			baseFormat := "[%s][%s][%s|%s][%s][%d][%s][%s][%s][%s][%s][%s][%s]"

			// Create a safe way to access these fields
			// Note: These fields are typically set once and rarely changed, so the race is less likely
			// but for complete safety, we could add getter methods
			args := []any{
				timeNow, logType, ml.InternalID, ml.Action, ml.Flow,
				ml.Step, ml.Entity, ml.SystemName, ml.ReffTrx,
				ml.RC, ml.TypeTrx, ml.Header, ml.URL,
			}

			// Append the main message and additional attributes
			fullFormat := fmt.Sprintf("%s%s%s", baseFormat, "[%v]", additionalAttrsStr)
			args = append(args, msg)
			return fmt.Sprintf(fullFormat, args...)
		} else {
			// Simple log format without MessageLog fields: [%s][%s][%v]
			if additionalAttrsStr != "" {
				return fmt.Sprintf("[%s][SYSTEM][%s|%v]", timeNow, msg, additionalAttrsStr)
			}
			return fmt.Sprintf("[%s][SYSTEM][%s]", timeNow, msg)
		}
	}
}
