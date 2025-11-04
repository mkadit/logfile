// logfile/specialized.go - Specialized logging functions
package logfile

import (
	"log/slog"
)

// logSpecialized is a helper function for specialized logging.
// It now uses getPayload to leverage object pooling and async-safe copying.
func logSpecialized(
	logger *MultLogger, // The primary logger (e.g., HTTPLogger)
	level LogLevel,
	eventType string,
	err error,
	message string,
	ml *MessageLog,
	async bool,
	specializedAttrs []slog.Attr, // Attributes specific to the specialized log type
	additionalAttrs []any, // Any extra attributes passed from the calling function
) {
	// isLoggerActive checks global shutdown and AppLogger status
	if Testing || !isLoggerActive() {
		return
	}

	// Lock to safely access AppLogger (for IndexLogger) and the passed-in logger
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	// Check if the primary logger itself is valid
	if logger == nil {
		return
	}

	// Combine specialized attributes and any additional attributes
	// We pre-allocate a single slice for efficiency
	allAttrs := make([]any, 0, len(specializedAttrs)+len(additionalAttrs))
	for _, attr := range specializedAttrs {
		allAttrs = append(allAttrs, attr)
	}
	allAttrs = append(allAttrs, additionalAttrs...)

	// Get a pooled, async-safe payload.
	// This function handles pooling, async copies of ml/attrs, and setting TimeNow.
	payload := getPayload(level, eventType, err, message, ml, allAttrs, async)

	// Log to the primary specialized logger
	payload.Logger = logger
	dispatchLog(payload, async)

	currentConfig := getConfigValue()
	// Log to index if centralized logging is enabled
	if currentConfig != nil && currentConfig.General.EnableCentralizedLogging && AppLogger.IndexLogger != nil {
		// Re-use the same payload struct value, just change the logger field.
		// dispatchLog takes by value, so this is safe.
		payload.Logger = AppLogger.IndexLogger
		// We keep the original eventType (e.g., "http") for the index
		dispatchLog(payload, async)
	}
}

// LogHTTP logs HTTP requests and responses.
func LogHTTP(async bool, method, url, status string, message string, attr ...any) {
	// We must RLock here to safely access AppLogger.HTTPLogger
	loggerMutex.RLock()
	if AppLogger == nil || AppLogger.HTTPLogger == nil {
		loggerMutex.RUnlock()
		return
	}
	// Get the specific logger to pass to the helper
	httpLogger := AppLogger.HTTPLogger
	loggerMutex.RUnlock() // Unlock *before* calling logSpecialized, as it will lock again

	httpAttrs := []slog.Attr{
		slog.String("method", method),
		slog.String("url", url),
		slog.String("status", status),
	}

	// Pass the specific logger to logSpecialized.
	// logSpecialized will handle its own locking to get the IndexLogger.
	logSpecialized(httpLogger, LevelInfo, "http", nil, message, nil, async, httpAttrs, attr)
}

// LogNMM logs ISO 8583 Network Management Messages (Sign-On, Echo-Test, etc.).
// It captures key ISO 8583 fields and routes to the NMMLogger.
func LogNMM(
	ml *MessageLog,
	async bool,
	mti, stan, processingCode, responseCode string,
	message string,
	attr ...any,
) {
	// We must RLock here to safely access AppLogger.NMMLogger
	loggerMutex.RLock()
	if AppLogger == nil || AppLogger.NMMLogger == nil {
		loggerMutex.RUnlock()
		return
	}
	// Get the specific logger to pass to the helper
	nmmLogger := AppLogger.NMMLogger
	loggerMutex.RUnlock() // Unlock *before* calling logSpecialized, as it will lock again

	nmmAttrs := []slog.Attr{
		slog.String("mti", mti),                  // e.g., "0800", "0810"
		slog.String("stan", stan),                // DE-11
		slog.String("proc_code", processingCode), // DE-3
		slog.String("rc_code", responseCode),     // DE-39
	}

	// Pass the specific logger to logSpecialized.
	// We pass the MessageLog (ml) for context, if one is provided.
	logSpecialized(nmmLogger, LevelInfo, "nmm", nil, message, ml, async, nmmAttrs, attr)
}
