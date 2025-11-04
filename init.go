// logfile/init.go - Logger initialization, worker pool, and shutdown.
// FIXED: Proper channel draining and flush
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

// devMode is a constant, presumably for checking development environment.
const devMode = "dev"

// NextMidnight calculates the duration until the next midnight.
// This is used to schedule the daily log rotation.
func NextMidnight() time.Duration {
	now := time.Now()
	// Calculate time for 00:00:00 tomorrow
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	// Return the duration from now until then
	return next.Sub(now)
}

// CreateLogger initializes the entire logging system.
// It sets up the configuration, creates the log channel, and starts the
// worker goroutines and the autoscaler.
// It is protected by loggerMutex to prevent concurrent initialization.
func CreateLogger() {
	// Don't initialize if shutdown is in progress
	if isShuttingDown.Load() {
		log.Println("WARNING: Cannot create logger during shutdown")
		return
	}

	// Acquire a full lock to modify global logger state
	loggerMutex.Lock()

	// Load config, or use default if none exists
	currentConfig := getConfigValue()
	if currentConfig == nil {
		defaultConfig := DefaultLogConfiguration()
		Config.Store(&defaultConfig)
		currentConfig = &defaultConfig
	}

	// Check if logChannel is already created (idempotency check)
	channelMutex.RLock()
	alreadyInitialized := logChannel != nil
	channelMutex.RUnlock()

	if alreadyInitialized {
		loggerMutex.Unlock() // Release lock before returning
		log.Println("WARNING: CreateLogger called, but logger is already active.")
		return
	}

	// Determine worker pool size from config, with sane defaults
	baseWorkers := 8
	maxWorkers := 32

	if currentConfig != nil {
		if currentConfig.General.WorkerPoolSize > 0 {
			baseWorkers = currentConfig.General.WorkerPoolSize
		}
		if currentConfig.General.MaxWorkerPoolSize > 0 {
			maxWorkers = currentConfig.General.MaxWorkerPoolSize
		}
	}

	// Create the buffered channel for async logging
	channelMutex.Lock()
	logChannel = make(chan LogPayload, currentConfig.General.LogChannel)
	channelMutex.Unlock()

	// Create the 'done' channel for the autoscaler
	scalerMutex.Lock()
	scalerDone = make(chan struct{})
	scalerMutex.Unlock()

	// Release the main logger lock
	loggerMutex.Unlock()

	// Start the initial set of worker goroutines
	for i := 0; i < baseWorkers; i++ {
		logWorkers.Add(1) // Increment WaitGroup counter
		go logWorker()
	}

	// Start the worker autoscaler goroutine
	logWorkers.Add(1)
	go workerAutoScaler(baseWorkers, maxWorkers)

	// Set up all file loggers based on the configuration
	if err := SetLogger(); err != nil {
		// Use standard log package for fatal error if logger setup fails
		Fatal(nil, err, "SYSTEM: logger cannot be set")
	}

	// Log that the logger is ready
	Info(nil, false, "logger created",
		slog.Group("data",
			slog.Any("config", currentConfig),
			slog.Int("initial_workers", baseWorkers),
			slog.Int("max_workers", maxWorkers),
		),
	)

	// Start a goroutine for daily log rotation
	if !Testing {
		go func() {
			for {
				if isShuttingDown.Load() {
					return
				}

				// Wait until next midnight
				nextRotation := time.After(NextMidnight())
				<-nextRotation

				if isShuttingDown.Load() {
					return
				}

				// Re-run SetLogger to create new files
				if err := SetLogger(); err != nil {
					log.Printf("SYSTEM: logger rotation failed: %v", err)
				} else {
					// Log rotation success to the *new* log file
					Info(nil, true, "log rotation success")
				}
			}
		}()
	}
}

// workerAutoScaler monitors the log channel usage and scales the number
// of worker goroutines up or down between baseWorkers and maxWorkers.
func workerAutoScaler(baseWorkers, maxWorkers int) {
	defer logWorkers.Done() // Signal WaitGroup on exit

	// Check channel usage every 3 seconds
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	currentWorkers := baseWorkers

	for {
		if isShuttingDown.Load() {
			return
		}

		// Get the scaler 'done' channel safely
		done, ok := getScalerDoneSafe()
		if !ok {
			// Channel is nil, system is shutting down
			return
		}

		select {
		case <-ticker.C:
			if isShuttingDown.Load() {
				return
			}

			// Get the log channel safely
			ch, ok := getLogChannelSafe()
			if !ok {
				// Channel is nil, system is shutting down
				return
			}

			// Calculate channel buffer usage
			usage := float64(len(ch)) / float64(cap(ch))

			// If usage is > 40% and we haven't hit max workers, scale up
			if usage > 0.4 && currentWorkers < maxWorkers {
				// Add up to 4 new workers at a time
				newWorkers := min(maxWorkers-currentWorkers, 4)
				for i := 0; i < newWorkers; i++ {
					logWorkers.Add(1)
					go logWorker()
					currentWorkers++
				}
			}
			// NOTE: This implementation does not scale *down* workers.
			// They exit naturally when the logChannel is closed during Shutdown.

		case <-done:
			// Shutdown signal received
			return
		}
	}
}

// logWorker is a goroutine that continuously reads from the logChannel
// and processes log messages.
func logWorker() {
	defer logWorkers.Done() // Signal WaitGroup on exit

	for {
		// Get channel safely
		ch, ok := getLogChannelSafe()
		if !ok {
			// Channel is nil, we're shutting down
			return
		}

		// Block and wait for a payload from the channel
		payload, stillOpen := <-ch
		if !stillOpen {
			// Channel was closed, exit the worker
			return
		}

		// Process the received log message
		processLogPayload(payload)
	}
}

// processLogPayload calls the internal logging function and returns
// the LogPayload object to the sync.Pool if pooling is enabled.
func processLogPayload(payload LogPayload) {
	// Call the function that actually writes the log
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

	// If object pooling is enabled, return the payload to the pool
	currentConfig := getConfigValue()
	if currentConfig != nil && currentConfig.General.EnableObjectPooling {
		putLogPayload(&payload)
	}
}

// Shutdown gracefully shuts down the logging system.
// It stops the autoscaler, closes the channel, waits for workers to finish,
// drains any remaining messages, and flushes all file buffers.
// It uses sync.Once to ensure this process only happens once.
func Shutdown() {
	shutdownOnce.Do(func() {
		// Set atomic flag to stop new log messages from being queued
		isShuttingDown.Store(true)

		// Stop the auto-scaler first
		scalerMutex.Lock()
		if scalerDone != nil {
			close(scalerDone)
			scalerDone = nil // Set to nil to signal it's closed
		}
		scalerMutex.Unlock()

		// Wait a brief moment for any in-flight logs to be sent to the channel
		time.Sleep(50 * time.Millisecond)

		// Get the channel *before* closing it
		channelMutex.Lock()
		ch := logChannel
		if ch != nil {
			// Close the channel to signal workers to stop
			close(ch)
			// Set global channel to nil so dispatchLog stops sending
			logChannel = nil
		}
		channelMutex.Unlock()

		// Wait for all worker goroutines (workers + autoscaler) to exit
		logWorkers.Wait()

		// Drain any messages that were still in the channel
		// This is a synchronous process
		if ch != nil {
			for {
				select {
				case payload, ok := <-ch:
					if !ok {
						// Channel is empty and closed
						goto drained
					}
					// Process the payload synchronously
					processLogPayload(payload)
				default:
					// Channel is empty
					goto drained
				}
			}
		}

	drained:
		// All messages processed, now flush file buffers
		FlushAll()
	})
}

// SetLogger initializes or reinitializes all loggers based on the current configuration.
// This function is called on startup by CreateLogger and by the rotation goroutine.
// It is protected by loggerMutex.
func SetLogger() error {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()

	oldAppLogger := AppLogger

	// If loggers already exist (e.g., from rotation), flush them
	if oldAppLogger != nil {
		flushLogger(oldAppLogger.MessageLogger)
		flushLogger(oldAppLogger.EventLogger)
		flushLogger(oldAppLogger.ErrorLogger)
		flushLogger(oldAppLogger.CriticalLogger)
		flushLogger(oldAppLogger.HTTPLogger)
		flushLogger(oldAppLogger.NMMLogger)
		flushLogger(oldAppLogger.DebugLogger)
		flushLogger(oldAppLogger.IndexLogger)
	}

	// Create new logger holders
	newAppLogger := &Loggers{}
	allMultLoggers := make(map[string]*MultLogger)
	currentConfig := getConfigValue()

	if currentConfig == nil {
		return fmt.Errorf("no configuration available")
	}

	// First Pass: Create all primary MultLogger instances
	// This pass sets up the primary writer (file or discard) for each log type.
	for logType, fileConfig := range currentConfig.Files {
		var primaryWriter io.Writer

		if !fileConfig.UseFileWriter {
			// If file writing is disabled, write to io.Discard
			primaryWriter = io.Discard
		} else if fileConfig.UseFileWriter {
			// Set up lumberjack for log rotation
			logFolder := filepath.Dir(fileConfig.Path)
			baseName := filepath.Base(fileConfig.Path)
			ext := filepath.Ext(baseName)
			nameWithoutExt := strings.TrimSuffix(baseName, ext)

			// Create log directory if it doesn't exist
			if err := os.MkdirAll(logFolder, 0700); err != nil {
				return fmt.Errorf("failed to create log directory %s: %w", logFolder, err)
			}

			// Create a dated filename for lumberjack
			datedFilename := filepath.Join(logFolder, fmt.Sprintf("%s.%s%s", time.Now().Format(TimeFormat), nameWithoutExt, ext))

			primaryWriter = &lumberjack.Logger{
				Filename:   datedFilename,
				MaxSize:    fileConfig.MaxSizeMB,
				MaxBackups: fileConfig.MaxBackups,
				MaxAge:     fileConfig.MaxAgeDays,
				Compress:   fileConfig.Compress,
			}
		} else {
			// Default to Stderr if not specified (though UseFileWriter covers this)
			primaryWriter = os.Stderr
		}

		// Determine the log level for this logger
		levelStr := fileConfig.MinLevel
		if levelStr == "" {
			// Fall back to the general level for this type
			levelStr = currentConfig.General.LevelsByType[logType]
		}
		level, err := ParseLogLevel(levelStr)
		if err != nil {
			return fmt.Errorf("invalid log level for type %s: %w", logType, err)
		}

		// Create the actual MultLogger instance
		ml, err := createMultLogger(logType, fileConfig, primaryWriter, level, currentConfig)
		if err != nil {
			return fmt.Errorf("failed to create logger for type %s: %w", logType, err)
		}

		// Store it in our map for the second pass
		allMultLoggers[logType] = ml

		// Assign it to the correct field in the new AppLogger struct
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

	// Second Pass: Wire up Additional Outputs
	// This pass iterates over the newly created loggers and connects them
	// to their additional targets (e.g., forwarding 'error' logs to 'message').
	for logType, ml := range allMultLoggers {
		ml.additionalSlogLoggers = make(map[OutputTarget]*slog.Logger)
		ml.additionalStdLoggers = make(map[OutputTarget]*log.Logger)

		// Wire up additional slog outputs
		for _, target := range ml.config.AdditionalOutputs.SlogOutputs {
			targetMl, ok := allMultLoggers[target.Type]
			if !ok {
				log.Printf("Warning: Additional slog output target '%s' for log type '%s' not found.", target.Type, logType)
				continue
			}

			// Create a new slog.Logger instance writing to the target's writer
			targetLevel := targetMl.level.ToSlogLevel()
			var handler slog.Handler
			if targetMl.config.Structured {
				handler = slog.NewJSONHandler(targetMl.writer, &slog.HandlerOptions{
					AddSource: currentConfig.General.AddSource,
					Level:     targetLevel,
					ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
						// Custom time formatting for slog
						if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindAny {
							if t, ok := a.Value.Any().(time.Time); ok {
								return slog.String(slog.TimeKey, t.Format(DefaultTimeFormat))
							}
						}
						return a
					},
				})
			} else {
				handler = slog.NewTextHandler(targetMl.writer, &slog.HandlerOptions{
					AddSource: currentConfig.General.AddSource,
					Level:     targetLevel,
				})
			}
			ml.additionalSlogLoggers[target] = slog.New(handler)
		}

		// Wire up additional standard log outputs
		for _, target := range ml.config.AdditionalOutputs.StdOutputs {
			targetMl, ok := allMultLoggers[target.Type]
			if !ok {
				log.Printf("Warning: Additional std output target '%s' for log type '%s' not found.", target.Type, logType)
				continue
			}
			// Create a new log.Logger instance writing to the target's writer
			ml.additionalStdLoggers[target] = log.New(targetMl.writer, "", 0)
		}
	}

	// Atomically swap the old logger struct with the new one
	AppLogger = newAppLogger

	// Wait a moment before returning to let old file handles be released
	if oldAppLogger != nil {
		time.Sleep(50 * time.Millisecond)
	}

	return nil
}

// flushLogger is a helper to safely flush a MultLogger.
func flushLogger(ml *MultLogger) {
	if ml != nil {
		ml.Flush()
	}
}

// createMultLogger builds a single MultLogger instance, configuring its
// standard and structured (slog) loggers.
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
		writer: primaryWriter, // This is the lumberjack.Logger or io.Discard
		level:  level,
	}

	// Configure the slog.Logger (StructuredLogger)
	if fileConfig.SlogWriter {
		// Handler for writing to the file (lumberjack)
		var fileHandler slog.Handler = slog.NewJSONHandler(ml.writer, &slog.HandlerOptions{
			AddSource: config.General.AddSource,
			Level:     level.ToSlogLevel(),
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				// Custom time formatting
				if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindAny {
					if t, ok := a.Value.Any().(time.Time); ok {
						return slog.String(slog.TimeKey, t.Format(DefaultTimeFormat))
					}
				}
				return a
			},
		})

		// Handler for writing to the console (os.Stderr)
		var consoleHandler slog.Handler
		if fileConfig.ConsoleStd {
			if config.General.PrettyPrint {
				// Use devslog for pretty, colorful console output
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
				// Use standard JSON handler for console
				consoleHandler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
					AddSource: config.General.AddSource,
					Level:     level.ToSlogLevel(),
				})
			}
		}

		// Combine file and console handlers if necessary
		var baseHandler slog.Handler
		if consoleHandler != nil {
			// Use multiHandler to write to both file and console
			baseHandler = &multiHandler{
				fileHandler:    fileHandler,
				consoleHandler: consoleHandler,
			}
		} else {
			// Just use the file handler
			baseHandler = fileHandler
		}

		// Wrap the handler with our SourceCorrectingHandler if AddSource is enabled
		var finalHandler slog.Handler
		if config.General.AddSource {
			finalHandler = &SourceCorrectingHandler{Handler: baseHandler}
		} else {
			finalHandler = baseHandler
		}

		ml.StructuredLogger = slog.New(finalHandler)
	} else {
		// If SlogWriter is false, create a logger that discards all messages
		ml.StructuredLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// Configure the log.Logger (StdLogger)
	if fileConfig.StdWriter {
		var stdWriters []io.Writer
		if fileConfig.UseFileWriter {
			stdWriters = append(stdWriters, ml.writer) // Add file writer
		}

		if fileConfig.ConsoleStd {
			stdWriters = append(stdWriters, os.Stderr) // Add console writer
		}

		if len(stdWriters) == 0 {
			// No writers specified, discard
			ml.StdLogger = log.New(io.Discard, "", 0)
		} else {
			// Use io.MultiWriter to write to all specified writers
			ml.StdLogger = log.New(io.MultiWriter(stdWriters...), "", 0)
		}
	} else {
		// If StdWriter is false, create a logger that discards all messages
		ml.StdLogger = log.New(io.Discard, "", 0)
	}

	return ml, nil
}

// multiHandler is a custom slog.Handler that multiplexes logs
// to two separate handlers (one for file, one for console).
type multiHandler struct {
	fileHandler    slog.Handler
	consoleHandler slog.Handler
}

// Enabled checks if *either* handler is enabled for the given level.
func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.fileHandler.Enabled(ctx, level) || (h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, level))
}

// Handle sends the log record to both handlers.
func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var err1, err2 error

	// Handle file log
	err1 = h.fileHandler.Handle(ctx, record)

	// Handle console log if it's enabled for this level
	if h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, record.Level) {
		err2 = h.consoleHandler.Handle(ctx, record)
	}

	// Return file error first if it exists
	if err1 != nil {
		return err1
	}
	// Otherwise return console error
	return err2
}

// WithAttrs returns a new multiHandler with attributes added to both sub-handlers.
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

// WithGroup returns a new multiHandler with a group added to both sub-handlers.
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

// SourceCorrectingHandler is a slog.Handler wrapper that finds the
// correct log call site (file and line) by walking up the stack.
// This is necessary because slog's AddSource feature would otherwise
// point to this logging library's code, not the user's code.
type SourceCorrectingHandler struct {
	slog.Handler
}

// Handle finds the correct runtime.Frame and sets r.PC before passing
// the record to the underlying handler.
func (h *SourceCorrectingHandler) Handle(ctx context.Context, r slog.Record) error {
	var pc uintptr
	var ok bool

	// Walk up the stack, max 20 frames
	for i := 2; i < 20; i++ {
		pc, _, _, ok = runtime.Caller(i)
		if !ok {
			// Stack walk failed
			return h.Handler.Handle(ctx, r)
		}

		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		name := fn.Name()

		// Skip frames that are inside slog or this logger package
		if strings.HasPrefix(name, "log/slog.") || strings.Contains(name, "/internal/logger.") {
			continue
		}

		// Found the correct caller, set the PC and pass to underlying handler
		r.PC = pc
		return h.Handler.Handle(ctx, r)
	}

	// Fallback
	return h.Handler.Handle(ctx, r)
}
