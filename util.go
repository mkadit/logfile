// logfile/util.go - Utility functions for logging operations
// FIXED: MessageLog snapshot instead of mutation, improved caller cache
package logfile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Caller cache variables
var (
	// callerCache maps program counter (PC) to a "file:line" string.
	// This avoids expensive runtime.FuncForPC calls on every log.
	callerCache = make(map[uintptr]string)
	// callerCacheMutex protects the callerCache from concurrent access.
	callerCacheMutex sync.RWMutex
	// callerCacheSize is the max number of entries before eviction.
	callerCacheSize = 1000
)

// deepCopyAny creates a deep copy of a value.
// This is crucial for async logging to prevent data races, where the
// calling goroutine modifies a map or slice after the log function returns,
// but before the logger goroutine has processed it.
func deepCopyAny(value any) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	// Basic types are passed by value, so they are safe.
	case string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool, time.Time, time.Duration:
		return v

	// --- Reference Types: These must be copied ---

	case map[string]any:
		if len(v) == 0 {
			return map[string]any{}
		}
		copied := make(map[string]any, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val) // Recursive copy
		}
		return copied

	case map[string]string:
		if len(v) == 0 {
			return map[string]string{}
		}
		return maps.Clone(v) // Use Go 1.21+ maps.Clone

	case map[int]string:
		if len(v) == 0 {
			return map[int]string{}
		}
		return maps.Clone(v)

	case map[int]time.Duration:
		// Special handling: convert to map[string]string for safer logging
		if len(v) == 0 {
			return map[string]string{}
		}
		copied := make(map[string]string, len(v))
		for k, val := range v {
			copied[fmt.Sprintf("step_%d", k)] = val.String()
		}
		return copied

	case map[int]any:
		if len(v) == 0 {
			return map[int]any{}
		}
		copied := make(map[int]any, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val) // Recursive copy
		}
		return copied

	case []any:
		if len(v) == 0 {
			return []any{}
		}
		copied := make([]any, len(v))
		for i, val := range v {
			copied[i] = deepCopyAny(val) // Recursive copy
		}
		return copied

	case []string:
		if len(v) == 0 {
			return []string{}
		}
		copied := make([]string, len(v))
		copy(copied, v)
		return copied

	case []int:
		if len(v) == 0 {
			return []int{}
		}
		copied := make([]int, len(v))
		copy(copied, v)
		return copied

	// Pointer types: copy the underlying value
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

	// Default: return the value as-is (e.g., custom structs).
	// This may not be race-safe if the struct contains reference types.
	default:
		return value
	}
}

// getCallerSource finds the source file and line number of the actual log call.
// It walks up the stack, skipping frames inside the logfile package.
// It uses a cache to speed up lookups.
func getCallerSource() slog.Attr {
	const maxDepth = 15

	for i := 2; i < maxDepth; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}

		// Check cache first (read lock)
		callerCacheMutex.RLock()
		if cached, found := callerCache[pc]; found {
			callerCacheMutex.RUnlock()
			return slog.String("source", cached)
		}
		callerCacheMutex.RUnlock()

		// Cache miss, get function name (expensive)
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			funcName := fn.Name()

			// Skip frames inside this package
			if !strings.Contains(funcName, "/logfile.") &&
				!strings.Contains(funcName, "logfile.Log") &&
				!strings.Contains(funcName, "logfile.log") {

				source := fmt.Sprintf("%s:%d", file, line)

				// Cache with write lock (check-lock-check pattern)
				callerCacheMutex.Lock()
				// Check again in case another goroutine added it while we waited for the lock
				if _, found := callerCache[pc]; !found {
					// Evict half the cache if full
					if len(callerCache) >= callerCacheSize {
						for k := range callerCache {
							delete(callerCache, k)
							if len(callerCache) <= callerCacheSize/2 {
								break
							}
						}
					}
					// Add to cache
					callerCache[pc] = source
				}
				callerCacheMutex.Unlock()

				return slog.String("source", source)
			}
		}
	}
	// Fallback
	return slog.String("source", "unknown")
}

// Flush forces any buffered logs in the writer (e.g., lumberjack) to be written.
func (m *MultLogger) Flush() error {
	// Check if the writer implements the Flusher interface
	if flusher, ok := m.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// FlushAll flushes all active loggers.
// This is called during graceful shutdown.
func FlushAll() error {
	loggerMutex.RLock() // Safe access to AppLogger
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

	// Iterate and flush all initialized loggers
	for _, logger := range loggers {
		if logger != nil {
			if err := logger.Flush(); err != nil {
				lastErr = err // Record last error
			}
		}
	}
	return lastErr
}

// logToSpecificLogger is the internal workhorse function that writes the log.
// It's called synchronously by dispatchLog (for sync calls) or by logWorker (for async calls).
// It formats the message for both structured (slog) and standard (log) writers.
func logToSpecificLogger(logger *MultLogger, level LogLevel, eventType string, err error, msg, timeNow string, ml *MessageLog, otherAttrs []any) {
	// Check if the logger is nil or the level is too low
	if logger == nil || !logger.IsEnabled(level) {
		return
	}

	// Get a reusable attribute slice from the pool
	slogAttrsPtr := getAttrSlice()
	slogAttrs := *slogAttrsPtr
	defer putAttrSlice(slogAttrsPtr) // Return to pool on exit

	// Add base attributes
	slogAttrs = append(slogAttrs,
		slog.String("event_type", eventType),
		slog.String("timestamp", timeNow),
	)

	if err != nil {
		// Include stack trace if available
		slogAttrs = append(slogAttrs, slog.String("error", fmt.Sprintf("%+v", err)))
	}

	// Add MessageLog attributes if present
	var currentStepDuration time.Duration
	if ml != nil {
		// Use the read-only snapshot method
		currentStepDuration = ml.GetCurrentStepDurationSnapshot()
		mlAttrs := ml.ToSlogAttrs() // Get all attributes from MessageLog

		// Add step timing information
		currentStep := ml.GetCurrentStep()
		if currentStep > 1 {
			slogAttrs = append(slogAttrs,
				slog.String("duration_step_active", currentStepDuration.String()))

			// Get duration of the previously completed step
			if prevDuration := ml.GetStepDuration(currentStep - 1); prevDuration > 0 {
				slogAttrs = append(slogAttrs,
					slog.String("duration_step_completed", prevDuration.String()))
			}
		}

		// Add performance flags
		totalDuration := ml.GetDurationSinceStart()
		if totalDuration > time.Second {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_slow", true))
		}
		if currentStep > 10 {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_many_steps", true))
		}

		slogAttrs = append(slogAttrs, mlAttrs...)
	}

	// --- Process 'otherAttrs' (from Info, Error, etc.) ---

	// This slice is for the standard (non-structured) logger
	originalAttrsForStd := make([]any, 0, len(otherAttrs))
	var firstComplexAttr any
	hasComplexAttr := false

	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			// It's already an slog.Attr
			var safeValue any
			// Deep copy if it's not a basic type
			if isBasicType(sa.Value.Any()) {
				safeValue = sa.Value.Any()
			} else {
				safeValue = deepCopyAny(sa.Value.Any())
				if !hasComplexAttr {
					firstComplexAttr = safeValue
					hasComplexAttr = true
				}
			}

			slogAttrs = append(slogAttrs, slog.Attr{
				Key:   sa.Key,
				Value: slog.AnyValue(safeValue),
			})

			// Format for standard logger
			originalAttrsForStd = append(originalAttrsForStd,
				fmt.Sprintf("%s: %v", sa.Key, safeValue))
		} else {
			// It's a plain 'any' type, treat it as a generic argument
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

	// --- Write to structured (slog) logger ---
	if logger.IsStructuredActive() {
		slogLevelToUse := level.ToSlogLevel()

		if logger.StructuredLogger != nil {
			logger.StructuredLogger.LogAttrs(context.Background(),
				slogLevelToUse, msg, slogAttrs...)
		}

		// Write to additional forwarded sloggers
		for target, slogger := range logger.additionalSlogLoggers {
			if slogger != nil && currentConfig != nil {
				targetConfig, ok := currentConfig.Files[target.Type]
				if !ok {
					continue
				}
				targetLevel, err := ParseLogLevel(targetConfig.MinLevel)
				if err != nil {
					continue
				}
				// Check if the forwarded logger's level is met
				if level >= targetLevel {
					slogger.LogAttrs(context.Background(), slogLevelToUse, msg, slogAttrs...)
				}
			}
		}
	}

	// --- Write to standard (log) logger ---
	if logger.IsStandardActive() {
		// Format the message for the standard logger
		formattedMsg := formatStandardLog(msg, timeNow, eventType, err, ml,
			originalAttrsForStd, firstComplexAttr, hasComplexAttr)

		if logger.StdLogger != nil {
			logger.StdLogger.Output(2, formattedMsg) // 2 = skip this frame
		}

		// Write to additional forwarded standard loggers
		for target, stdlogger := range logger.additionalStdLoggers {
			if stdlogger != nil && currentConfig != nil {
				targetConfig, ok := currentConfig.Files[target.Type]
				if !ok {
					continue
				}
				targetLevel, err := ParseLogLevel(targetConfig.MinLevel)
				if err != nil {
					continue
				}
				// Check if the forwarded logger's level is met
				if level >= targetLevel {
					stdlogger.Output(2, formattedMsg)
				}
			}
		}
	}
}

// formatStandardLog creates a formatted string for standard logging.
// This replicates a more traditional log format.
func formatStandardLog(msg, timeNow, logType string, err error, ml *MessageLog,
	otherAttrs []any, firstComplexAttr any, hasComplexAttr bool,
) string {
	additionalAttrs := make([]string, 0, len(otherAttrs)+3)

	// Add source if enabled
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.AddSource {
		sourceAttr := getCallerSource()
		additionalAttrs = append(additionalAttrs,
			fmt.Sprintf("source=%v", sourceAttr.Value.Any()))
	}

	// Add MessageLog durations if present
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

	// Add other attributes
	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			additionalAttrs = append(additionalAttrs,
				fmt.Sprintf("%s=%v", sa.Key, sa.Value.Any()))
		} else {
			additionalAttrs = append(additionalAttrs, fmt.Sprintf("%v", a))
		}
	}

	// Join all additional attributes
	additionalAttrsStr := ""
	if len(additionalAttrs) > 0 {
		additionalAttrsStr = " " + strings.Join(additionalAttrs, " ")
	}

	// Format with error
	if err != nil {
		if hasComplexAttr {
			// Special format for complex attributes + error
			return fmt.Sprintf("[%s][%s][%T|%+v|%s|%+v][%v]",
				timeNow, logType, firstComplexAttr, firstComplexAttr,
				msg, WithStack(err), additionalAttrsStr)
		}
		// Standard error format
		return fmt.Sprintf("[%s][%s][%s|%+v][%v]",
			timeNow, logType, msg, WithStack(err), additionalAttrsStr)
	}

	// Format with MessageLog context
	if ml != nil {
		return fmt.Sprintf("[%s][%s][%s|%s][%s][%d][%s][%s][%s][%s][%s][%s][%s][%v]%s",
			timeNow, logType, ml.InternalID, ml.Action, ml.Flow,
			ml.GetCurrentStep(), ml.Entity, ml.SystemName, ml.ReffTrx,
			ml.RC, ml.TypeTrx, ml.Header, ml.URL, msg, additionalAttrsStr)
	}

	// Format for simple system message
	if additionalAttrsStr != "" {
		return fmt.Sprintf("[%s][SYSTEM][%s|%v]", timeNow, msg, additionalAttrsStr)
	}
	return fmt.Sprintf("[%s][SYSTEM][%s]", timeNow, msg)
}

// isBasicType checks if a value is a basic (non-reference) type.
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

// JSONFormatter converts log data to JSON strings that match slog.JSONHandler output.
type JSONFormatter struct {
	addSource  bool
	timeFormat string
}

// NewJSONFormatter creates a JSONFormatter with the given configuration.
func NewJSONFormatter(addSource bool, timeFormat string) *JSONFormatter {
	return &JSONFormatter{
		addSource:  addSource,
		timeFormat: timeFormat,
	}
}

// FormatRecord converts log data to JSON matching slog.JSONHandler output.
func (f *JSONFormatter) FormatRecord(level LogLevel, eventType string, err error, msg, timeNow string, ml *MessageLog, otherAttrs []any) ([]byte, error) {
	// Build the log record data structure
	record := make(map[string]any, 32) // Pre-allocate capacity

	// Add base fields
	record["event_type"] = eventType
	record["timestamp"] = timeNow

	// Add level based on the log type (for consistency with slog)
	if level != LevelInfo {
		record["level"] = level.String()
	}

	if err != nil {
		record["error"] = fmt.Sprintf("%+v", err)
	}

	// Add MessageLog attributes if present
	if ml != nil {
		// Use read-only snapshot method
		currentStepDuration := ml.GetCurrentStepDurationSnapshot()
		mlAttrs := ml.ToSlogAttrs()

		// Add step timing information
		currentStep := ml.GetCurrentStep()
		if currentStep > 1 {
			record["duration_step_active"] = currentStepDuration.String()

			// Get duration of the previously completed step
			if prevDuration := ml.GetStepDuration(currentStep - 1); prevDuration > 0 {
				record["duration_step_completed"] = prevDuration.String()
			}
		}

		// Add performance flags
		totalDuration := ml.GetDurationSinceStart()
		if totalDuration > time.Second {
			record["performance_flag_slow"] = true
		}
		if currentStep > 10 {
			record["performance_flag_many_steps"] = true
		}

		// Add all MessageLog attributes
		for _, attr := range mlAttrs {
			switch attr.Value.Kind() {
			case slog.KindString:
				record[attr.Key] = attr.Value.String()
			case slog.KindInt64:
				record[attr.Key] = attr.Value.Int64()
			case slog.KindUint64:
				record[attr.Key] = attr.Value.Uint64()
			case slog.KindFloat64:
				record[attr.Key] = attr.Value.Float64()
			case slog.KindBool:
				record[attr.Key] = attr.Value.Bool()
			case slog.KindTime:
				record[attr.Key] = f.formatTime(attr.Value.Time())
			case slog.KindAny:
				record[attr.Key] = f.formatAny(attr.Value.Any())
			}
		}
	}

	// Process 'otherAttrs' (from Info, Error, etc.)
	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			// It's already an slog.Attr
			var safeValue any
			// Deep copy if it's not a basic type
			if isBasicType(sa.Value.Any()) {
				safeValue = sa.Value.Any()
			} else {
				safeValue = deepCopyAny(sa.Value.Any())
			}

			switch sa.Value.Kind() {
			case slog.KindString:
				record[sa.Key] = safeValue.(string)
			case slog.KindInt64:
				record[sa.Key] = sa.Value.Int64()
			case slog.KindUint64:
				record[sa.Key] = sa.Value.Uint64()
			case slog.KindFloat64:
				record[sa.Key] = sa.Value.Float64()
			case slog.KindBool:
				record[sa.Key] = sa.Value.Bool()
			case slog.KindTime:
				record[sa.Key] = f.formatTime(sa.Value.Time())
			case slog.KindAny:
				record[sa.Key] = f.formatAny(safeValue)
			}
		} else {
			// It's a plain 'any' type
			var safeCopy any
			if isBasicType(a) {
				safeCopy = a
			} else {
				safeCopy = deepCopyAny(a)
			}

			if safeCopy != nil {
				record["arg"] = f.formatAny(safeCopy)
			}
		}
	}

	// Add source if enabled
	if f.addSource {
		sourceAttr := getCallerSource()
		if sourceAttr.Value.Kind() == slog.KindString {
			record[sourceAttr.Key] = sourceAttr.Value.String()
		}
	}

	// Add the main message
	record["msg"] = msg

	// Marshal to JSON
	return json.Marshal(record)
}

// formatTime formats time values according to the formatter's time format.
func (f *JSONFormatter) formatTime(t time.Time) string {
	return t.Format(f.timeFormat)
}

// formatAny handles complex types for JSON marshaling.
func (f *JSONFormatter) formatAny(value any) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case map[string]any:
		// Recursively format nested maps
		result := make(map[string]any, len(v))
		for k, val := range v {
			result[k] = f.formatAny(val)
		}
		return result
	case map[int]any:
		// Convert int keys to strings for JSON compatibility
		result := make(map[string]any, len(v))
		for k, val := range v {
			result[fmt.Sprintf("key_%d", k)] = f.formatAny(val)
		}
		return result
	case map[int]string:
		result := make(map[string]any, len(v))
		for k, val := range v {
			result[fmt.Sprintf("key_%d", k)] = val
		}
		return result
	case map[int]time.Duration:
		result := make(map[string]any, len(v))
		for k, val := range v {
			result[fmt.Sprintf("step_%d", k)] = val.String()
		}
		return result
	case []any:
		// Recursively format slices
		result := make([]any, len(v))
		for i, val := range v {
			result[i] = f.formatAny(val)
		}
		return result
	case []string:
		// Convert string slices directly
		result := make([]any, len(v))
		for i, val := range v {
			result[i] = val
		}
		return result
	case []int:
		result := make([]any, len(v))
		for i, val := range v {
			result[i] = val
		}
		return result
	case time.Duration:
		return v.String()
	default:
		// For basic types and custom structs, return as-is
		return v
	}
}
