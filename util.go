package logfile

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"
)

// NEW: Cache for caller source to reduce runtime.Caller overhead
var (
	callerCache      = make(map[uintptr]string)
	callerCacheMutex sync.RWMutex
	callerCacheSize  = 1000 // Limit cache size
)

// toAnySlice converts a slice of slog.Attr to []any
func toAnySlice(attrs []slog.Attr) []any {
	anySlice := make([]any, len(attrs))
	for i, v := range attrs {
		anySlice[i] = v
	}
	return anySlice
}

// OPTIMIZED: Type-specific deep copy without JSON marshaling
func deepCopyAny(value any) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	// Fast path for basic types (no copy needed)
	case string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool, time.Time, time.Duration:
		return v

	// Map types
	case map[string]any:
		if len(v) == 0 {
			return map[string]any{}
		}
		copied := make(map[string]any, len(v))
		for k, val := range v {
			copied[k] = deepCopyAny(val)
		}
		return copied

	case map[string]string:
		if len(v) == 0 {
			return map[string]string{}
		}
		copied := make(map[string]string, len(v))
		for k, val := range v {
			copied[k] = val
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

	case map[int]time.Duration:
		if len(v) == 0 {
			return map[string]string{}
		}
		// Convert to string map for safe serialization
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

	// Slice types
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
		copy(copied, v)
		return copied

	case []int:
		if len(v) == 0 {
			return []int{}
		}
		copied := make([]int, len(v))
		copy(copied, v)
		return copied

	// Pointer types - dereference and copy
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

	default:
		// For unknown types, return as-is
		// This is safer than JSON marshaling for performance
		return value
	}
}

// OPTIMIZED: Cached caller source lookup
func getCallerSource() slog.Attr {
	const maxDepth = 15 // Reduced from 20

	for i := 2; i < maxDepth; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}

		// Check cache first
		callerCacheMutex.RLock()
		if cached, found := callerCache[pc]; found {
			callerCacheMutex.RUnlock()
			return slog.String("source", cached)
		}
		callerCacheMutex.RUnlock()

		fn := runtime.FuncForPC(pc)
		if fn != nil {
			funcName := fn.Name()

			// Skip logfile package functions
			if !strings.Contains(funcName, "/logfile.") &&
				!strings.Contains(funcName, "logfile.Log") &&
				!strings.Contains(funcName, "logfile.log") {

				source := fmt.Sprintf("%s:%d", file, line)

				// Cache the result
				callerCacheMutex.Lock()
				if len(callerCache) < callerCacheSize {
					callerCache[pc] = source
				}
				callerCacheMutex.Unlock()

				return slog.String("source", source)
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

// OPTIMIZED: Streamlined logging with object pooling
func logToSpecificLogger(logger *MultLogger, level LogLevel, eventType string, err error, msg, timeNow string, ml *MessageLog, otherAttrs []any) {
	if logger == nil || !logger.IsEnabled(level) {
		return
	}

	// Get attribute slice from pool
	slogAttrsPtr := getAttrSlice()
	slogAttrs := *slogAttrsPtr
	defer putAttrSlice(slogAttrsPtr)

	// Pre-allocate with known size
	slogAttrs = append(slogAttrs,
		slog.String("event_type", eventType),
		slog.String("timestamp", timeNow),
	)

	if err != nil {
		slogAttrs = append(slogAttrs, slog.String("error", err.Error()))
	}

	// Handle MessageLog with thread-safe operations
	var currentStepDuration time.Duration
	if ml != nil {
		currentStepDuration = ml.SafeRecordStepDuration()
		mlAttrs := ml.ToSlogAttrs()

		if ml.Step > 1 {
			slogAttrs = append(slogAttrs,
				slog.String("duration_step_active", currentStepDuration.String()))

			if prevDuration := ml.GetStepDuration(ml.Step - 1); prevDuration > 0 {
				slogAttrs = append(slogAttrs,
					slog.String("duration_step_completed", prevDuration.String()))
			}
		}

		totalDuration := ml.GetDurationSinceStart()
		if totalDuration > time.Second {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_slow", true))
		}
		if ml.Step > 10 {
			slogAttrs = append(slogAttrs, slog.Bool("performance_flag_many_steps", true))
		}

		slogAttrs = append(slogAttrs, mlAttrs...)
	}

	// OPTIMIZED: Process attributes without unnecessary allocations
	originalAttrsForStd := make([]any, 0, len(otherAttrs))
	var firstComplexAttr any
	hasComplexAttr := false

	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			// Only deep copy if necessary
			var safeValue any
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

			originalAttrsForStd = append(originalAttrsForStd,
				fmt.Sprintf("%s: %v", sa.Key, safeValue))
		} else {
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

	// Log with structured logger
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

	// Log with standard logger
	if logger.IsStandardActive() {
		formattedMsg := formatStandardLog(msg, timeNow, eventType, err, ml,
			originalAttrsForStd, firstComplexAttr, hasComplexAttr)

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

// OPTIMIZED: Streamlined standard log formatting
func formatStandardLog(msg, timeNow, logType string, err error, ml *MessageLog,
	otherAttrs []any, firstComplexAttr any, hasComplexAttr bool,
) string {
	// Pre-allocate with estimated capacity
	additionalAttrs := make([]string, 0, len(otherAttrs)+3)

	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.AddSource {
		sourceAttr := getCallerSource()
		additionalAttrs = append(additionalAttrs,
			fmt.Sprintf("source=%v", sourceAttr.Value.Any()))
	}

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

	for _, a := range otherAttrs {
		if sa, ok := a.(slog.Attr); ok {
			additionalAttrs = append(additionalAttrs,
				fmt.Sprintf("%s=%v", sa.Key, sa.Value.Any()))
		} else {
			additionalAttrs = append(additionalAttrs, fmt.Sprintf("%v", a))
		}
	}

	additionalAttrsStr := ""
	if len(additionalAttrs) > 0 {
		additionalAttrsStr = " " + strings.Join(additionalAttrs, " ")
	}

	if err != nil {
		if hasComplexAttr {
			return fmt.Sprintf("[%s][%s][%T|%+v|%s|%+v][%v]",
				timeNow, logType, firstComplexAttr, firstComplexAttr,
				msg, WithStack(err), additionalAttrsStr)
		}
		return fmt.Sprintf("[%s][%s][%s|%+v][%v]",
			timeNow, logType, msg, WithStack(err), additionalAttrsStr)
	}

	if ml != nil {
		return fmt.Sprintf("[%s][%s][%s|%s][%s][%d][%s][%s][%s][%s][%s][%s][%s][%v]%s",
			timeNow, logType, ml.InternalID, ml.Action, ml.Flow,
			ml.Step, ml.Entity, ml.SystemName, ml.ReffTrx,
			ml.RC, ml.TypeTrx, ml.Header, ml.URL, msg, additionalAttrsStr)
	}

	if additionalAttrsStr != "" {
		return fmt.Sprintf("[%s][SYSTEM][%s|%v]", timeNow, msg, additionalAttrsStr)
	}
	return fmt.Sprintf("[%s][SYSTEM][%s]", timeNow, msg)
}

// isBasicType checks if a value is a basic Go type
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
