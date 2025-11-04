// logfile/init.go - Logger initialization, worker pool, and shutdown.
package logfile

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/golang-cz/devslog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// devMode is a constant string for development mode.
const devMode = "dev"

// NextMidnight calculates the duration until the next midnight (00:00).
// This is used to schedule daily log rotation.
func NextMidnight() time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	duration := next.Sub(now)

	return duration
}

// CreateLogger initializes the entire logging system.
// It sets up the configuration, creates the log channel, and starts the
// worker goroutines and the auto-scaler.
func CreateLogger() {
	// Lock to protect global state during initialization.
	loggerMutex.Lock()

	// Load default config if none is loaded.
	currentConfig := getConfigValue()
	if currentConfig == nil {
		defaultConfig := DefaultLogConfiguration()
		Config = &defaultConfig
		currentConfig = &defaultConfig
	}

	// Prevent re-initialization if already active.
	if logChannel != nil {
		loggerMutex.Unlock()
		log.Println("WARNING: CreateLogger called, but logger is already active.")
		return
	}

	// Create the buffered channel for asynchronous logs.
	logChannel = make(chan LogPayload, currentConfig.General.LogChannel)
	// Create a channel to signal the scaler goroutine to stop.
	scalerDone = make(chan struct{})

	// Get worker pool configuration.
	baseWorkers := 8
	maxWorkers := 32

	if Config != nil {
		if Config.General.WorkerPoolSize > 0 {
			baseWorkers = Config.General.WorkerPoolSize
		}
		if Config.General.MaxWorkerPoolSize > 0 {
			maxWorkers = Config.General.MaxWorkerPoolSize
		}
	}

	// Unlock before starting goroutines to avoid holding lock.
	loggerMutex.Unlock()

	// Start the base number of workers.
	for i := 0; i < baseWorkers; i++ {
		logWorkers.Add(1)
		go logWorker()
	}

	// Start the dynamic worker auto-scaler goroutine.
	logWorkers.Add(1)
	go func() {
		defer logWorkers.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		currentWorkers := baseWorkers

		for {
			// Safely read the scalerDone channel.
			loggerMutex.RLock()
			done := scalerDone
			loggerMutex.RUnlock()

			// Check if shutdown has been initiated.
			if done == nil {
				return // scalerDone was set to nil by Shutdown.
			}

			select {
			case <-ticker.C:
				// Check channel usage.
				loggerMutex.RLock()
				ch := logChannel
				if ch == nil {
					// logChannel was set to nil by Shutdown.
					loggerMutex.RUnlock()
					return
				}
				// Calculate buffer usage percentage.
				usage := float64(len(ch)) / float64(cap(ch))
				loggerMutex.RUnlock()

				// Scale up: If buffer is > 40% full, add more workers.
				if usage > 0.4 && currentWorkers < maxWorkers {
					// Add up to 4 new workers, respecting maxWorkers limit.
					newWorkers := min(maxWorkers-currentWorkers, 4)
					for i := 0; i < newWorkers; i++ {
						logWorkers.Add(1)
						go logWorker()
						currentWorkers++
					}
				}

				// Scale down: If buffer is < 10% full, reduce workers (not implemented).
				// This implementation only scales up.
				if usage < 0.1 && currentWorkers > baseWorkers {
					// This line is present but won't reduce goroutines,
					// it just resets the internal counter.
					// A real scale-down would require a worker pool implementation.
					currentWorkers = max(baseWorkers, currentWorkers-2)
				}

			case <-done:
				// Shutdown signal received.
				return
			}
		}
	}()

	// Initialize all logger instances (e.g., MessageLogger, ErrorLogger).
	if err := SetLogger(); err != nil {
		Fatal(nil, err, "SYSTEM: logger cannot be set")
	}

	// Log that the system is ready (synchronously).
	Info(nil, false, "logger created",
		slog.Group("data",
			slog.Any("config", currentConfig),
			slog.Int("initial_workers", baseWorkers),
			slog.Int("max_workers", maxWorkers),
		),
	)

	// Start the daily log rotation goroutine.
	if !Testing {
		go func() {
			for {
				// Wait until the next midnight.
				nextRotation := time.After(NextMidnight())
				<-nextRotation
				// Re-run SetLogger to create new files.
				if err := SetLogger(); err != nil {
					log.Printf("SYSTEM: logger rotation failed: %v", err)
				} else {
					Info(nil, true, "log rotation success")
				}
			}
		}()
	}
}

// logWorker is the function executed by each worker goroutine.
// It blocks on the logChannel, processes messages, and returns them to the pool.
func logWorker() {
	defer logWorkers.Done()

	for {
		// Safely read the logChannel.
		loggerMutex.RLock()
		ch := logChannel
		loggerMutex.RUnlock()

		if ch == nil {
			return // Channel is nil, system is shutting down.
		}

		// Read the next log payload from the channel.
		payload, ok := <-ch
		if !ok {
			return // Channel is closed, system is shutting down.
		}

		// Process the log entry.
		logToSpecificLogger(
			payload.Logger,
			payload.Level,
			payload.EventType,
			payload.Err,
			payload.Msg,
			payload.TimeNow,
			payload.Ml,
			payload.OtherAttrs,
		)

		// Return the LogPayload object to the pool for reuse.
		if Config != nil && Config.General.EnableObjectPooling {
			putLogPayload(&payload)
		}
	}
}

// Shutdown gracefully shuts down the logging system.
// It signals all workers to stop, waits for them to finish, and flushes buffers.
func Shutdown() {
	loggerMutex.Lock()
	if logChannel == nil {
		loggerMutex.Unlock()
		return // Already shut down or never initialized.
	}

	// Signal the auto-scaler to stop.
	if scalerDone != nil {
		close(scalerDone)
		scalerDone = nil // Set to nil to signal scaler has been stopped.
	}

	// Close the log channel to signal workers to stop.
	close(logChannel)
	logChannel = nil // Set to nil to prevent new logs.

	loggerMutex.Unlock()

	// Wait for all worker goroutines (and the scaler) to exit.
	logWorkers.Wait()

	// Flush any remaining buffers in the underlying writers (e.g., lumberjack).
	FlushAll()
}

// SetLogger initializes or reinitializes all loggers based on the current configuration.
// This is used at startup and for log rotation.
func SetLogger() error {
	loggerMutex.Lock() // Full lock to replace all logger instances.
	defer loggerMutex.Unlock()

	// Close existing writers to flush buffers before replacing them.
	if AppLogger != nil {
		if AppLogger.MessageLogger != nil {
			AppLogger.MessageLogger.Flush()
		}
		if AppLogger.EventLogger != nil {
			AppLogger.EventLogger.Flush()
		}
		if AppLogger.ErrorLogger != nil {
			AppLogger.ErrorLogger.Flush()
		}
		if AppLogger.CriticalLogger != nil {
			AppLogger.CriticalLogger.Flush()
		}
		if AppLogger.HTTPLogger != nil {
			AppLogger.HTTPLogger.Flush()
		}
		if AppLogger.NMMLogger != nil {
			AppLogger.NMMLogger.Flush()
		}
		if AppLogger.DebugLogger != nil {
			AppLogger.DebugLogger.Flush()
		}
		if AppLogger.IndexLogger != nil {
			AppLogger.IndexLogger.Flush()
		}
	}

	newAppLogger := &Loggers{}
	allMultLoggers := make(map[string]*MultLogger)
	currentConfig := getConfigValue()

	// --- First Pass: Create all primary MultLoggers ---
	// This pass creates all the logger instances and their primary writers.
	for logType, fileConfig := range currentConfig.Files {

		var primaryWriter io.Writer
		if !fileConfig.UseFileWriter {
			// If file writing is disabled, discard all output.
			primaryWriter = io.Discard
		} else if fileConfig.UseFileWriter {
			// Configure lumberjack for file-based logging and rotation.
			logFolder := filepath.Dir(fileConfig.Path)
			baseName := filepath.Base(fileConfig.Path)
			ext := filepath.Ext(baseName)
			nameWithoutExt := strings.TrimSuffix(baseName, ext)

			// Ensure log directory exists.
			if err := os.MkdirAll(logFolder, 0700); err != nil {
				return fmt.Errorf("failed to create log directory %s: %w", logFolder, err)
			}

			// Create a date-stamped filename for lumberjack.
			datedFilename := filepath.Join(logFolder, fmt.Sprintf("%s.%s%s", time.Now().Format(TimeFormat), nameWithoutExt, ext))

			primaryWriter = &lumberjack.Logger{
				Filename:   datedFilename, // The file to write to.
				MaxSize:    fileConfig.MaxSizeMB,
				MaxBackups: fileConfig.MaxBackups,
				MaxAge:     fileConfig.MaxAgeDays,
				Compress:   fileConfig.Compress,
			}
		} else {
			// Default to Stderr if not using file and not discarded.
			primaryWriter = os.Stderr
		}

		// Determine the log level for this logger.
		levelStr := fileConfig.MinLevel
		if levelStr == "" {
			levelStr = currentConfig.General.LevelsByType[logType]
		}
		level, err := ParseLogLevel(levelStr)
		if err != nil {
			return fmt.Errorf("invalid log level for type %s: %w", logType, err)
		}

		// Create the MultLogger instance.
		ml, err := createMultLogger(
			logType,
			fileConfig,
			primaryWriter,
			level,
			currentConfig,
		)
		if err != nil {
			return fmt.Errorf("failed to create logger for type %s: %w", logType, err)
		}
		// Store it in the map for the second pass.
		allMultLoggers[logType] = ml

		// Assign it to the correct field in the AppLogger struct.
		switch logType {
		case "message":
			newAppLogger.MessageLogger = ml
		case "event":
			newAppLogger.EventLogger = ml
		case "error":
			newAppLogger.ErrorLogger = ml
		case "critical":
			newAppLogger.CriticalLogger = ml
		case "http":
			newAppLogger.HTTPLogger = ml
		case "nmm":
			newAppLogger.NMMLogger = ml
		case "debug":
			newAppLogger.DebugLogger = ml
		case "index":
			newAppLogger.IndexLogger = ml
		}
	}

	// --- Second Pass: Wire up Additional Outputs ---
	// This pass connects loggers to *other* loggers (e.g., 'error' logs
	// also go to 'message'). This must be done after all loggers exist.
	for logType, ml := range allMultLoggers {
		ml.additionalSlogLoggers = make(map[OutputTarget]*slog.Logger)
		ml.additionalStdLoggers = make(map[OutputTarget]*log.Logger)

		// Slog Outputs
		for _, target := range ml.config.AdditionalOutputs.SlogOutputs {
			// Find the target logger instance from the map.
			targetMl, ok := allMultLoggers[target.Type]
			if !ok {
				log.Printf("Warning: Additional slog output target '%s' for log type '%s' not found.", target.Type, logType)
				continue
			}
			targetLevel := targetMl.level.ToSlogLevel()
			var handler slog.Handler
			if targetMl.config.Structured {
				// Create a JSON handler writing to the target's writer.
				handler = slog.NewJSONHandler(targetMl.writer, &slog.HandlerOptions{
					AddSource: currentConfig.General.AddSource,
					Level:     targetLevel,
					ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
						// Custom time formatting.
						if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindAny {
							if t, ok := a.Value.Any().(time.Time); ok {
								return slog.String(slog.TimeKey, t.Format(DefaultTimeFormat))
							}
						}
						return a
					},
				})
			} else {
				// Create a Text handler.
				handler = slog.NewTextHandler(targetMl.writer, &slog.HandlerOptions{
					AddSource: currentConfig.General.AddSource,
					Level:     targetLevel,
				})
			}
			// Store the new slog.Logger instance.
			ml.additionalSlogLoggers[target] = slog.New(handler)
		}

		// Std Outputs
		for _, target := range ml.config.AdditionalOutputs.StdOutputs {
			// Find the target logger instance.
			targetMl, ok := allMultLoggers[target.Type]
			if !ok {
				log.Printf("Warning: Additional std output target '%s' for log type '%s' not found.", target.Type, logType)
				continue
			}
			// Create a new log.Logger writing to the target's writer.
			ml.additionalStdLoggers[target] = log.New(targetMl.writer, "", 0)
		}
	}

	// Atomically swap the old AppLogger with the new one.
	AppLogger = newAppLogger
	return nil
}

// createMultLogger is a helper to create a MultLogger instance based on FileConfig.
func createMultLogger(
	logType string,
	fileConfig FileConfig,
	primaryWriter io.Writer,
	level LogLevel,
	config *LogConfiguration,
) (*MultLogger, error) {
	ml := &MultLogger{
		name:   logType,
		config: fileConfig,
		writer: primaryWriter,
		level:  level,
	}

	// Slog Logger Setup
	if fileConfig.SlogWriter {
		// Handler for writing to the primary file writer (lumberjack).
		var fileHandler slog.Handler = slog.NewJSONHandler(ml.writer, &slog.HandlerOptions{
			AddSource: config.General.AddSource,
			Level:     level.ToSlogLevel(),
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				// Custom time formatting for file logs.
				if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindAny {
					if t, ok := a.Value.Any().(time.Time); ok {
						return slog.String(slog.TimeKey, t.Format(DefaultTimeFormat))
					}
				}
				return a
			},
		})

		// Handler for writing to the console (stderr).
		var consoleHandler slog.Handler
		if fileConfig.ConsoleStd {
			if config.General.PrettyPrint {
				// Use 'devslog' for pretty-printed, human-readable console output.
				opts := &devslog.Options{
					HandlerOptions: &slog.HandlerOptions{
						AddSource: config.General.AddSource,
						Level:     level.ToSlogLevel(),
					},
					MaxSlicePrintSize: 4,
					SortKeys:          true,
					NewLineAfterLog:   true,
					StringerFormatter: true,
				}
				consoleHandler = devslog.NewHandler(os.Stderr, opts)
			} else {
				// Use standard JSON handler for console.
				consoleHandler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
					AddSource: config.General.AddSource,
					Level:     level.ToSlogLevel(),
				})
			}
		}

		// Combine file and console handlers if both are active.
		var baseHandler slog.Handler
		if consoleHandler != nil {
			baseHandler = &multiHandler{
				fileHandler:    fileHandler,
				consoleHandler: consoleHandler,
			}
		} else {
			baseHandler = fileHandler
		}

		// Wrap with the SourceCorrectingHandler if AddSource is enabled.
		var finalHandler slog.Handler
		if config.General.AddSource {
			finalHandler = &SourceCorrectingHandler{Handler: baseHandler}
		} else {
			finalHandler = baseHandler
		}

		ml.StructuredLogger = slog.New(finalHandler)

	} else {
		// If SlogWriter is disabled, create a logger that discards all writes.
		ml.StructuredLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// Standard Logger (log.Logger) Setup
	if fileConfig.StdWriter {
		var stdWriters []io.Writer
		if fileConfig.UseFileWriter {
			stdWriters = append(stdWriters, ml.writer)
		}

		if fileConfig.ConsoleStd {
			stdWriters = append(stdWriters, os.Stderr)
		}

		if len(stdWriters) == 0 {
			// No output, discard.
			ml.StdLogger = log.New(io.Discard, "", 0)
		} else {
			// Create a logger that writes to all specified writers.
			ml.StdLogger = log.New(io.MultiWriter(stdWriters...), "", 0)
		}
	} else {
		// If StdWriter is disabled, discard all writes.
		ml.StdLogger = log.New(io.Discard, "", 0)
	}

	return ml, nil
}

// multiHandler is an slog.Handler that writes to both a file handler and a console handler.
type multiHandler struct {
	fileHandler    slog.Handler
	consoleHandler slog.Handler
}

// Enabled checks if *either* handler is enabled for the given level.
func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.fileHandler.Enabled(ctx, level) || (h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, level))
}

// Handle sends the log record to both the file and console handlers.
func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var err1, err2 error

	// Handle file log.
	err1 = h.fileHandler.Handle(ctx, record)

	// Handle console log, if enabled.
	if h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, record.Level) {
		err2 = h.consoleHandler.Handle(ctx, record)
	}

	// Return the first error encountered.
	if err1 != nil {
		return err1
	}
	return err2
}

// WithAttrs returns a new multiHandler with the attributes added to both sub-handlers.
func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newFileHandler := h.fileHandler.WithAttrs(attrs)
	var newConsoleHandler slog.Handler
	if h.consoleHandler != nil {
		newConsoleHandler = h.consoleHandler.WithAttrs(attrs)
	}
	return &multiHandler{
		fileHandler:    newFileHandler,
		consoleHandler: newConsoleHandler,
	}
}

// WithGroup returns a new multiHandler with the group added to both sub-handlers.
func (h *multiHandler) WithGroup(name string) slog.Handler {
	newFileHandler := h.fileHandler.WithGroup(name)
	var newConsoleHandler slog.Handler
	if h.consoleHandler != nil {
		newConsoleHandler = h.consoleHandler.WithGroup(name)
	}
	return &multiHandler{
		fileHandler:    newFileHandler,
		consoleHandler: newConsoleHandler,
	}
}

// SourceCorrectingHandler is a slog.Handler that wraps another handler
// to correct the source code location (file:line) of the log record.
// It walks the call stack to find the first frame *outside* this logging package.
type SourceCorrectingHandler struct {
	slog.Handler
}

// Handle finds the correct call frame and updates the record's PC before
// passing it to the underlying handler.
func (h *SourceCorrectingHandler) Handle(ctx context.Context, r slog.Record) error {
	var pc uintptr
	var ok bool

	// Search up the stack for the call site.
	for i := 2; i < 20; i++ { // Start at 2 to skip runtime.Caller and this Handle.
		pc, _, _, ok = runtime.Caller(i)
		if !ok {
			// Stack walk failed.
			return h.Handler.Handle(ctx, r)
		}

		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		name := fn.Name()

		// Skip frames that are part of slog or this package.
		if strings.HasPrefix(name, "log/slog.") || strings.Contains(name, "/internal/logger.") {
			continue
		}

		// Found the correct frame. Update the record's PC and pass it on.
		r.PC = pc
		return h.Handler.Handle(ctx, r)
	}

	// Fallback to default handling if no suitable frame was found.
	return h.Handler.Handle(ctx, r)
}
