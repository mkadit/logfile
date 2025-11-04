// logfile/util.go - Utility functions for logging operations
package logfile

import (
	"context"  // For passing context to slog handlers
	"fmt"      // For formatted string operations
	"log/slog" // Structured logging
	"runtime"  // For accessing call stack information
	"strings"  // For string manipulation
	"sync"     // For thread-safe operations
	"time"     // For time-related operations
)

// Cache for caller source information to reduce runtime.Caller overhead.
var (
	// callerCache maps program counters (PC) to formatted "file:line" strings.
	callerCache = make(map[uintptr]string)

	// callerCacheMutex protects concurrent access to callerCache.
	callerCacheMutex sync.RWMutex

	// callerCacheSize limits cache size to prevent unbounded growth.
	callerCacheSize = 1000
)

// toAnySlice converts a slice of slog.Attr to []any.
// This is a helper for functions that expect []any.
func toAnySlice(attrs []slog.Attr) []any {
	anySlice := make([]any, len(attrs))
	for i, v := range attrs {
		anySlice[i] = v
	}
	return anySlice
}

// deepCopyAny creates a deep copy of a value to prevent data races.
// This is crucial when logging maps or slices from multiple goroutines,
// as the logger may still be processing the value after the caller
// goroutine modifies it.
func deepCopyAny(value any) any {
	if value == nil {
		return nil
	}

	// Type switch for efficient copying based on actual type.
	switch v := value.(type) {
	// Basic types are immutable or passed by value, so no copy is needed.
	case string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool, time.Time, time.Duration:
		return v

	// --- Map types ---
	case map[string]any:
		if len(v) == 0 {
			return map[string]any{} // Return empty map, not nil
		}
		copied := make(map[string]any, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val) // Recursively copy values
		}
		return copied

	case map[string]string:
		if len(v) == 0 {
			return map[string]string{}
		}
		copied := make(map[string]string, len(v))
		for k, val := range v {
			copied[k] = val // Strings are immutable
		}
		return copied

	case map[int]string:
		if len(v) == 0 {
			return map[int]string{}
		}
		copied := make(map[int]string, len(v))
		for k, val := range v {
			copied[k] = val
		}
		return copied

	// Special case for step durations, convert to string map.
	case map[int]time.Duration:
		if len(v) == 0 {
			return map[string]string{}
		}
		copied := make(map[string]string, len(v))
		for k, val := range v {
			copied[fmt.Sprintf("step_%d", k)] = val.String()
		}
		return copied

	case map[int]interface{}:
		if len(v) == 0 {
			return map[int]interface{}{}
		}
		copied := make(map[int]interface{}, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val)
		}
		return copied

	// --- Slice types ---
	case []any:
		if len(v) == 0 {
			return []any{}
		}
		copied := make([]any, len(v))
		for i, val := range v {
			copied[i] = deepCopyAny(val)
		}
		return copied

	case []string:
		if len(v) == 0 {
			return []string{}
		}
		copied := make([]string, len(v))
		copy(copied, v) // Efficient copy
		return copied

	case []int:
		if len(v) == 0 {
			return []int{}
		}
		copied := make([]int, len(v))
		copy(copied, v) // Efficient copy
		return copied

	// --- Pointer types ---
	case *string:
		if v == nil {
			return nil
		}
		s := *v
		return &s

	case *int:
		if v == nil {
			return nil
		}
		i := *v
		return &i

	// --- Default ---
	default:
		// For unknown complex types, return as-is.
		// The caller is responsible for ensuring thread-safety.
		return value
	}
}

// getCallerSource finds the source file and line number of the *actual* log call.
// It walks the stack, skipping frames inside the logfile package.
// This is expensive and should only be enabled in development.
func getCallerSource() slog.Attr {
	const maxDepth = 15

	// Walk up the call stack.
	for i := 2; i < maxDepth; i++ { // Start at 2 to skip runtime.Caller, getCallerSource
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break // No more frames.
		}

		// Check cache first for this program counter.
		callerCacheMutex.RLock()
		if cached, found := callerCache[pc]; found {
			callerCacheMutex.RUnlock()
			return slog.String("source", cached)
		}
		callerCacheMutex.RUnlock()

		// Get function info.
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			funcName := fn.Name()

			// Skip internal logfile functions. We want the *caller* of logfile.
			if !strings.Contains(funcName, "/logfile.") &&
				!strings.Contains(funcName, "logfile.Log") &&
				!strings.Contains(funcName, "logfile.log") {

				// Found it. Format as "file:line".
				source := fmt.Sprintf("%s:%d", file, line)

				// Cache the result.
				callerCacheMutex.Lock()
				if len(callerCache) < callerCacheSize {
					callerCache[pc] = source
				}
				callerCacheMutex.Unlock()

				return slog.String("source", source)
			}
		}
	}
	// Fallback if we couldn't find the source.
	return slog.String("source", "unknown")
}

// Flush forces any buffered logs in the logger's writer to be written.
// This is useful for file-based writers like lumberjack.
func (m *MultLogger) Flush() error {
	// Check if the writer implements a Flush() error interface.
	if flusher, ok := m.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// FlushAll flushes all active loggers.
// This should be called before application shutdown.
func FlushAll() error {
	loggerMutex.RLock() // Read lock to safely access AppLogger.
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

	// Flush each logger that exists.
	for _, logger := range loggers {
		if logger != nil {
			if err := logger.Flush(); err != nil {
				lastErr = err // Keep track of the last error.
			}
		}
	}
	return lastErr
}

// logToSpecificLogger is the internal "workhorse" function.
// It takes a fully formed LogPayload and writes it to the
// standard and/or structured loggers defined in the MultLogger.
func logToSpecificLogger(logger *MultLogger, level LogLevel, eventType string, err error, msg, timeNow string, ml *MessageLog, otherAttrs []any) {
	// Early return if logger is nil or level is below threshold.
	if logger == nil || !logger.IsEnabled(level) {
		return
	}

	// Get a reusable attribute slice from the pool.
	slogAttrsPtr := getAttrSlice()
	slogAttrs := *slogAttrsPtr
	defer putAttrSlice(slogAttrsPtr) // Always return to pool.

	// Add standard attributes.
	slogAttrs = append(slogAttrs,
		slog.String("event_type", eventType),
		slog.String("timestamp", timeNow),
	)

	// Add error if present.
	if err != nil {
		slogAttrs = append(slogAttrs, slog.String("error", err.Error()))
	}

	// Handle MessageLog context if provided.
	var currentStepDuration time.Duration
	if ml != nil {
		// Record duration of the *current* step *at this exact moment*.
		currentStepDuration = ml.SafeRecordStepDuration()
		// Convert MessageLog fields to attributes.
		mlAttrs := ml.ToSlogAttrs()

		// Add step timing information.
		if ml.Step > 1 {
			slogAttrs = append(slogAttrs,
				slog.String("duration_step_active", currentStepDuration.String()))

			// Add previous step's *completed* duration.
			if prevDuration := ml.GetStepDuration(ml.Step - 1); prevDuration > 0 {
				slogAttrs = append(slogAttrs,
					slog.String("duration_step_completed", prevDuration.String()))
			}
		}

		// Add performance flags for slow or complex operations.
		totalDuration := ml.GetDurationSinceStart()
		if totalDuration > time.Second {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_slow", true))
		}
		if ml.Step > 10 {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_many_steps", true))
		}

		// Add all MessageLog attributes.
		slogAttrs = append(slogAttrs, mlAttrs...)
	}

	// Process additional user-provided attributes.
	originalAttrsForStd := make([]any, 0, len(otherAttrs))
	var firstComplexAttr any
	hasComplexAttr := false

	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			// It's already an slog.Attr.
			var safeValue any
			if isBasicType(sa.Value.Any()) {
				safeValue = sa.Value.Any() // Basic types are safe.
			} else {
				safeValue = deepCopyAny(sa.Value.Any()) // Deep copy complex types.
				if !hasComplexAttr {
					firstComplexAttr = safeValue
					hasComplexAttr = true
				}
			}

			// Add to structured log attributes.
			slogAttrs = append(slogAttrs, slog.Attr{
				Key:   sa.Key,
				Value: slog.AnyValue(safeValue),
			})

			// Format for standard (plain text) logger.
			originalAttrsForStd = append(originalAttrsForStd,
				fmt.Sprintf("%s: %v", sa.Key, safeValue))
		} else {
			// It's a plain `any` value.
			var safeCopy any
			if isBasicType(a) {
				safeCopy = a
			} else {
				safeCopy = deepCopyAny(a)
				if !hasComplexAttr {
					firstComplexAttr = safeCopy
					hasComplexAttr = true
				}
			}

			slogAttrs = append(slogAttrs, slog.Any("arg", safeCopy))
			originalAttrsForStd = append(originalAttrsForStd, safeCopy)
		}
	}

	currentConfig := getConfigValue()

	// --- Write to structured logger (slog) ---
	if logger.IsStructuredActive() {
		slogLevelToUse := level.ToSlogLevel()

		// Log to primary structured logger.
		if logger.StructuredLogger != nil {
			logger.StructuredLogger.LogAttrs(context.Background(),
				slogLevelToUse, msg, slogAttrs...)
		}

		// Log to additional structured outputs (redirection).
		for target, slogger := range logger.additionalSlogLoggers {
			if slogger != nil {
				// Check if this target's level is met.
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

	// --- Write to standard logger (log.Logger) ---
	if logger.IsStandardActive() {
		// Format the log entry as a single plain-text string.
		formattedMsg := formatStandardLog(msg, timeNow, eventType, err, ml,
			originalAttrsForStd, firstComplexAttr, hasComplexAttr)

		// Log to primary standard logger.
		if logger.StdLogger != nil {
			logger.StdLogger.Output(2, formattedMsg)
		}

		// Log to additional standard outputs (redirection).
		for target, stdlogger := range logger.additionalStdLoggers {
			if stdlogger != nil {
				// Check if this target's level is met.
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

// formatStandardLog creates a formatted string for standard (non-structured) logging.
func formatStandardLog(msg, timeNow, logType string, err error, ml *MessageLog,
	otherAttrs []any, firstComplexAttr any, hasComplexAttr bool,
) string {
	// Pre-allocate slice for additional attributes.
	additionalAttrs := make([]string, 0, len(otherAttrs)+3)

	// Add source location if enabled.
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.AddSource {
		sourceAttr := getCallerSource()
		additionalAttrs = append(additionalAttrs,
			fmt.Sprintf("source=%v", sourceAttr.Value.Any()))
	}

	// Add timing from MessageLog if available.
	if ml != nil {
		if totalDuration := ml.GetDurationSinceStart(); totalDuration > 0 {
			additionalAttrs = append(additionalAttrs,
				fmt.Sprintf("total_duration=%v", totalDuration))
		}
		if stepDuration := ml.GetDurationSinceLastLog(); stepDuration > 0 {
			additionalAttrs = append(additionalAttrs,
				fmt.Sprintf("step_duration=%v", stepDuration))
		}
	}

	// Format other attributes.
	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			additionalAttrs = append(additionalAttrs,
				fmt.Sprintf("%s=%v", sa.Key, sa.Value.Any()))
		} else {
			additionalAttrs = append(additionalAttrs, fmt.Sprintf("%v", a))
		}
	}

	// Join attributes with spaces.
	additionalAttrsStr := ""
	if len(additionalAttrs) > 0 {
		additionalAttrsStr = " " + strings.Join(additionalAttrs, " ")
	}

	// Format based on what information is available.
	if err != nil {
		// Error format (includes stack trace via WithStack).
		if hasComplexAttr {
			return fmt.Sprintf("[%s][%s][%T|%+v|%s|%+v][%v]",
				timeNow, logType, firstComplexAttr, firstComplexAttr,
				msg, WithStack(err), additionalAttrsStr)
		}
		return fmt.Sprintf("[%s][%s][%s|%+v][%v]",
			timeNow, logType, msg, WithStack(err), additionalAttrsStr)
	}

	if ml != nil {
		// MessageLog format (rich context).
		return fmt.Sprintf("[%s][%s][%s|%s][%s][%d][%s][%s][%s][%s][%s][%s][%s][%v]%s",
			timeNow, logType, ml.InternalID, ml.Action, ml.Flow,
			ml.Step, ml.Entity, ml.SystemName, ml.ReffTrx,
			ml.RC, ml.TypeTrx, ml.Header, ml.URL, msg, additionalAttrsStr)
	}

	// Simple system message format.
	if additionalAttrsStr != "" {
		return fmt.Sprintf("[%s][SYSTEM][%s|%v]", timeNow, msg, additionalAttrsStr)
	}
	return fmt.Sprintf("[%s][SYSTEM][%s]", timeNow, msg)
}

// isBasicType checks if a value is a basic Go type that is immutable
// or passed by value, making it safe for concurrent use without copying.
func isBasicType(val any) bool {
	switch val.(type) {
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, complex64, complex128,
		string, time.Time, time.Duration:
		return true
	default:
		return false
	}
}
