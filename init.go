// logfile/init.go - Fixed logger initialization with new configuration structure
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

const devMode = "dev"

// NextMidnight calculates the duration until the next midnight.
func NextMidnight() time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	duration := next.Sub(now)

	return duration
}

// Enhanced CreateLogger with dynamic worker scaling
func CreateLogger() {
	currentConfig := getConfigValue()
	if currentConfig == nil {
		defaultConfig := DefaultLogConfiguration()
		Config = &defaultConfig
		currentConfig = &defaultConfig
	}

	// Create channel
	logChannel = make(chan LogPayload, currentConfig.General.LogChannel)

	// Start with base number of workers
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

	// Start base workers
	for i := 0; i < baseWorkers; i++ {
		logWorkers.Add(1)
		go logWorker()
	}

	// OPTIMIZED: More aggressive worker scaling
	go func() {
		ticker := time.NewTicker(3 * time.Second) // Reduced from 5s
		defer ticker.Stop()

		currentWorkers := baseWorkers

		for range ticker.C {
			usage := float64(len(logChannel)) / float64(cap(logChannel))

			// Scale up more aggressively
			if usage > 0.4 && currentWorkers < maxWorkers {
				newWorkers := min(maxWorkers-currentWorkers, 4) // Add 4 at a time
				for i := 0; i < newWorkers; i++ {
					logWorkers.Add(1)
					go logWorker()
					currentWorkers++
				}
			}

			// Scale down when idle
			if usage < 0.1 && currentWorkers > baseWorkers {
				// Workers will naturally exit when channel is empty
				currentWorkers = max(baseWorkers, currentWorkers-2)
			}
		}
	}()

	// Rest of your existing CreateLogger code...
	if err := SetLogger(); err != nil {
		Fatal(nil, err, "SYSTEM: logger cannot be set")
	}

	Info(nil, false, "logger created",
		slog.Group("data",
			slog.Any("config", currentConfig),
			slog.Int("initial_workers", baseWorkers),
			slog.Int("max_workers", maxWorkers),
		),
	)

	// Log rotation goroutine (unchanged)
	if !Testing {
		go func() {
			for {
				nextRotation := time.After(NextMidnight())
				<-nextRotation
				if err := SetLogger(); err != nil {
					log.Printf("SYSTEM: logger rotation failed: %v", err)
				} else {
					Info(nil, true, "log rotation success")
				}
			}
		}()
	}
}

// OPTIMIZED: logWorker with object pooling
func logWorker() {
	defer logWorkers.Done()

	for payload := range logChannel {
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

		// Return payload to pool if pooling is enabled
		if Config != nil && Config.General.EnableObjectPooling {
			putLogPayload(&payload)
		}
	}
}

// Shutdown gracefully shuts down the logging system, ensuring all async logs are written.
// This should be called before the application exits.
func Shutdown() {
	close(logChannel) // Close the channel to signal workers to stop
	logWorkers.Wait() // Wait for all worker goroutines to finish processing
	FlushAll()        // Final flush of all underlying writers
}

// SetLogger initializes or reinitializes all loggers based on the current configuration.
func SetLogger() error {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()

	// Close existing writers before creating new ones
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
		if AppLogger.IndexLogger != nil { // New: Flush index logger
			AppLogger.IndexLogger.Flush()
		}
	}

	newAppLogger := &Loggers{}
	allMultLoggers := make(map[string]*MultLogger) // Map to store all created MultLoggers by type
	currentConfig := getConfigValue()

	// --- First Pass: Create all primary MultLoggers ---
	for logType, fileConfig := range currentConfig.Files {

		// Determine the primary writer based on UseFileWriter and ConsoleStd
		var primaryWriter io.Writer
		if !fileConfig.UseFileWriter {
			// If both are false, write to Discard
			primaryWriter = io.Discard
		} else if fileConfig.UseFileWriter {
			logFolder := filepath.Dir(fileConfig.Path)
			baseName := filepath.Base(fileConfig.Path)
			ext := filepath.Ext(baseName)
			nameWithoutExt := strings.TrimSuffix(baseName, ext)

			// Create directory if it doesn't exist
			if err := os.MkdirAll(logFolder, 0700); err != nil {
				return fmt.Errorf("failed to create log directory %s: %w", logFolder, err)
			}

			// Construct the filename with the current date (e.g., "logs/Debug/Debug.2024-06-19.log")
			// TimeFormat is assumed to be defined in types.go (e.g., time.DateOnly "2006-01-02")
			datedFilename := filepath.Join(logFolder, fmt.Sprintf("%s.%s%s", time.Now().Format(TimeFormat), nameWithoutExt, ext))

			primaryWriter = &lumberjack.Logger{
				Filename:   datedFilename,
				MaxSize:    fileConfig.MaxSizeMB,
				MaxBackups: fileConfig.MaxBackups,
				MaxAge:     fileConfig.MaxAgeDays,
				Compress:   fileConfig.Compress,
			}
		} else { // Only ConsoleStd is true (or UseFileWriter is false but ConsoleStd is true)
			primaryWriter = os.Stderr
		}

		// Use MinLevel from FileConfig, fall back to LevelsByType if not specified
		levelStr := fileConfig.MinLevel
		if levelStr == "" {
			levelStr = currentConfig.General.LevelsByType[logType]
		}
		level, err := ParseLogLevel(levelStr)
		if err != nil {
			return fmt.Errorf("invalid log level for type %s: %w", logType, err)
		}

		ml, err := createMultLogger(
			logType,
			fileConfig,    // Pass the full fileConfig
			primaryWriter, // Pass the determined primaryWriter
			level,
			currentConfig, // Pass the full Config to access General.AddSource
		)
		if err != nil {
			return fmt.Errorf("failed to create logger for type %s: %w", logType, err)
		}
		allMultLoggers[logType] = ml

		// Assign to newAppLogger fields
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
		case "index": // New: Assign index logger
			newAppLogger.IndexLogger = ml
		}
	}

	// --- Second Pass: Wire up Additional Outputs and pre-create loggers ---
	for logType, ml := range allMultLoggers {
		ml.additionalSlogLoggers = make(map[OutputTarget]*slog.Logger)
		ml.additionalStdLoggers = make(map[OutputTarget]*log.Logger)

		// Slog Outputs
		for _, target := range ml.config.AdditionalOutputs.SlogOutputs {
			targetMl, ok := allMultLoggers[target.Type]
			if !ok {
				log.Printf("Warning: Additional slog output target '%s' for log type '%s' not found.", target.Type, logType)
				continue
			}
			// Pre-create slog.Logger for the target's primary writer
			targetLevel := targetMl.level.ToSlogLevel() // Use target's own level
			var handler slog.Handler
			if targetMl.config.Structured {
				handler = slog.NewJSONHandler(targetMl.writer, &slog.HandlerOptions{
					AddSource: currentConfig.General.AddSource,
					Level:     targetLevel,
					ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
						// Add a timestamp to the slog output for additional writers if not present
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

		// Std Outputs
		for _, target := range ml.config.AdditionalOutputs.StdOutputs {
			targetMl, ok := allMultLoggers[target.Type]
			if !ok {
				log.Printf("Warning: Additional std output target '%s' for log type '%s' not found.", target.Type, logType)
				continue
			}
			// Pre-create standard log.Logger for the target's primary writer
			ml.additionalStdLoggers[target] = log.New(targetMl.writer, "", 0) // Standard logger usually doesn't need explicit formatting options here
		}
	}

	AppLogger = newAppLogger // Assign the fully configured loggers
	return nil
}

// createMultLogger helper to create a MultLogger instance based on FileConfig.
func createMultLogger(
	logType string,
	fileConfig FileConfig,
	primaryWriter io.Writer, // Now explicitly passed
	level LogLevel,
	config *LogConfiguration, // Pass the entire config
) (*MultLogger, error) {
	ml := &MultLogger{
		name:   logType,
		config: fileConfig,
		writer: primaryWriter,
		level:  level,
	}

	// Slog Logger Setup
	if fileConfig.SlogWriter {
		// --- Step 1: Create the individual handlers without correction ---

		// Create the handler for the JSON file output
		var fileHandler slog.Handler = slog.NewJSONHandler(ml.writer, &slog.HandlerOptions{
			AddSource: config.General.AddSource,
			Level:     level.ToSlogLevel(),
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindAny {
					if t, ok := a.Value.Any().(time.Time); ok {
						return slog.String(slog.TimeKey, t.Format(DefaultTimeFormat))
					}
				}
				return a
			},
		})

		// Create the handler for the console (pretty-print or JSON)
		var consoleHandler slog.Handler
		if fileConfig.ConsoleStd {
			if config.General.PrettyPrint {
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
				consoleHandler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
					AddSource: config.General.AddSource,
					Level:     level.ToSlogLevel(),
				})
			}
		}

		// --- Step 2: Combine handlers if needed ---

		var baseHandler slog.Handler
		if consoleHandler != nil {
			// If we have both file and console, combine them with multiHandler
			baseHandler = &multiHandler{
				fileHandler:    fileHandler,
				consoleHandler: consoleHandler,
			}
		} else {
			// Otherwise, the base handler is just the file handler
			baseHandler = fileHandler
		}

		// This is the crucial change. We wrap the baseHandler (which could be
		// the multiHandler or just the fileHandler) to correct the source
		// for ALL destinations.
		var finalHandler slog.Handler
		if config.General.AddSource {
			// If AddSource is true, use the expensive handler for correctness.
			finalHandler = &SourceCorrectingHandler{Handler: baseHandler}
		} else {
			// If AddSource is false, use the fast, unwrapped handler for performance.
			finalHandler = baseHandler
		}

		ml.StructuredLogger = slog.New(finalHandler)

	} else {
		// If SlogWriter is false, ensure structuredLogger is completely disabled
		ml.StructuredLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// Standard Logger Setup (This part remains the same)
	if fileConfig.StdWriter {
		var stdWriters []io.Writer
		if fileConfig.UseFileWriter {
			stdWriters = append(stdWriters, ml.writer)
		}

		if fileConfig.ConsoleStd {
			stdWriters = append(stdWriters, os.Stderr)
		}

		if len(stdWriters) == 0 {
			ml.StdLogger = log.New(io.Discard, "", 0)
		} else {
			ml.StdLogger = log.New(io.MultiWriter(stdWriters...), "", 0)
		}
	} else {
		ml.StdLogger = log.New(io.Discard, "", 0)
	}

	return ml, nil
}

// multiHandler is an slog.Handler that writes to both a file handler and a console handler.
type multiHandler struct {
	fileHandler    slog.Handler
	consoleHandler slog.Handler
}

// Enabled reports whether the handler handles records at the given level.
func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// A message is enabled if either the file handler or the console handler enables it.
	// This ensures that even if one is disabled by level, the other might still log.
	return h.fileHandler.Enabled(ctx, level) || (h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, level))
}

// Handle handles the Record.
func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var err1, err2 error

	// Always handle by file handler
	err1 = h.fileHandler.Handle(ctx, record)

	// Handle console logging if enabled by its own Enabled method
	if h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, record.Level) {
		err2 = h.consoleHandler.Handle(ctx, record)
	}

	// Return the first error encountered, if any
	if err1 != nil {
		return err1
	}
	return err2
}

// WithAttrs returns a new Handler whose attributes consist of h's attributes followed by attrs.
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

// WithGroup returns a new Handler whose group settings consist of h's group settings followed by group.
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

// Place this in init.go or handler.go, replacing the old SourceCorrectingHandler

// SourceCorrectingHandler is a slog.Handler that wraps another handler to correct
// the source code location of the log record.
type SourceCorrectingHandler struct {
	slog.Handler
}

// Handle corrects the source location by filtering known logging packages.
func (h *SourceCorrectingHandler) Handle(ctx context.Context, r slog.Record) error {
	var pc uintptr
	var ok bool

	// We start at frame 2, because:
	// Frame 0 is runtime.Callers itself.
	// Frame 1 is this Handle function.
	for i := 2; i < 20; i++ {
		pc, _, _, ok = runtime.Caller(i)
		if !ok {
			// If we can't find a caller, we'll just pass the record as-is.
			return h.Handler.Handle(ctx, r)
		}

		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		name := fn.Name()

		if strings.HasPrefix(name, "log/slog.") || strings.Contains(name, "/internal/logger.") {
			continue
		}

		// The first function that is not part of the logging system is the true source.
		// We update the record's Program Counter (PC) and stop searching.
		r.PC = pc
		return h.Handler.Handle(ctx, r)
	}

	// Fallback in case we can't find the caller.
	return h.Handler.Handle(ctx, r)
}
