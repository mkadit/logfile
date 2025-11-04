// logfile/performance_tracker.go - High-performance duration tracking utilities
package logfile

import (
	"log/slog"
	"sync"
	"time"
)

// DurationTracker provides high-performance duration tracking with minimal overhead.
// It is a simpler alternative to MessageLog, focused purely on timing steps.
// It is safe for concurrent use.
type DurationTracker struct {
	operationStart time.Time    // The time the tracker was created.
	lastStep       time.Time    // The time NextStep was last called.
	stepCount      int          // The number of completed steps.
	operationName  string       // The name of the operation being tracked.
	mu             sync.RWMutex // Protects fields during concurrent access.
}

// NewDurationTracker creates and starts a new duration tracker.
// 'operationName' is used for logging.
func NewDurationTracker(operationName string) *DurationTracker {
	now := time.Now()
	return &DurationTracker{
		operationStart: now,
		lastStep:       now,
		stepCount:      0,
		operationName:  operationName,
	}
}

// NextStep records the completion of a step and returns its duration.
// It resets the step timer for the *next* step.
// This method is thread-safe.
func (dt *DurationTracker) NextStep() time.Duration {
	dt.mu.Lock() // Use full lock as we are modifying fields.
	defer dt.mu.Unlock()

	now := time.Now()
	stepDuration := now.Sub(dt.lastStep)
	dt.lastStep = now
	dt.stepCount++
	return stepDuration
}

// TotalDuration returns the total time elapsed since the tracker was created.
// This method is thread-safe.
func (dt *DurationTracker) TotalDuration() time.Duration {
	dt.mu.RLock() // Use read lock.
	defer dt.mu.RUnlock()
	return time.Since(dt.operationStart)
}

// CurrentStepDuration returns the time elapsed since the last step was completed.
// This method is thread-safe.
func (dt *DurationTracker) CurrentStepDuration() time.Duration {
	dt.mu.RLock() // Use read lock.
	defer dt.mu.RUnlock()
	return time.Since(dt.lastStep)
}

// StepCount returns the number of steps that have been completed.
// This method is thread-safe.
func (dt *DurationTracker) StepCount() int {
	dt.mu.RLock() // Use read lock.
	defer dt.mu.RUnlock()
	return dt.stepCount
}

// ToSlogAttrs converts the tracker's current state to a slice of slog.Attr
// for easy inclusion in structured logs.
// This method is thread-safe.
func (dt *DurationTracker) ToSlogAttrs() []slog.Attr {
	dt.mu.RLock() // Use read lock.
	defer dt.mu.RUnlock()

	return []slog.Attr{
		slog.String("operation_name", dt.operationName),
		slog.String("total_duration", dt.TotalDuration().String()),
		// Note: This calculates duration since lastStep *now*, not when NextStep was called.
		slog.String("current_step_duration", time.Since(dt.lastStep).String()),
		slog.Int("step_count", dt.stepCount),
		slog.Time("operation_start", dt.operationStart),
	}
}

// LogStep is a helper function that calls NextStep() and logs the result
// using the Info function.
func (dt *DurationTracker) LogStep(ml *MessageLog, async bool, stepName, message string, attr ...any) {
	// Record the step, which advances the tracker.
	stepDuration := dt.NextStep()

	// Prepare attributes for this step log.
	stepAttrs := []any{
		slog.String("step_name", stepName),
		slog.String("step_duration", stepDuration.String()),
		slog.String("total_duration", dt.TotalDuration().String()),
		// StepCount is now the step just completed.
		slog.Int("step_number", dt.StepCount()),
	}
	stepAttrs = append(stepAttrs, attr...)

	// Log the step information.
	Info(ml, async, message, stepAttrs...)
}

// LogComplete is a helper function that logs the completion of the operation.
// It calls NextStep() one final time to capture the duration of the last step.
func (dt *DurationTracker) LogComplete(ml *MessageLog, async bool, message string, attr ...any) {
	// Record the final step.
	finalStepDuration := dt.NextStep()
	totalDuration := dt.TotalDuration()

	// Prepare attributes for the completion log.
	completeAttrs := []any{
		slog.String("operation_complete", dt.operationName),
		slog.String("final_step_duration", finalStepDuration.String()),
		slog.String("total_duration", totalDuration.String()),
		slog.Int("total_steps", dt.StepCount()),
	}
	completeAttrs = append(completeAttrs, attr...)

	Debug(ml, async, message, completeAttrs...)
}

// PerformanceBenchmark collects duration samples for a specific operation
// to calculate statistics (min, max, avg).
// It uses a ring buffer to store the last 'maxSamples' durations.
// It is safe for concurrent use.
type PerformanceBenchmark struct {
	name       string
	samples    []time.Duration // A slice acting as a ring buffer.
	maxSamples int             // The maximum number of samples to store.
	mu         sync.Mutex      // Protects the samples slice.
}

// NewPerformanceBenchmark creates a new benchmark tracker with a fixed sample size.
func NewPerformanceBenchmark(name string, maxSamples int) *PerformanceBenchmark {
	return &PerformanceBenchmark{
		name: name,
		// Pre-allocate capacity for the ring buffer
		samples:    make([]time.Duration, 0, maxSamples),
		maxSamples: maxSamples,
	}
}

// Record adds a new duration sample to the benchmark.
// If the sample size exceeds maxSamples, the oldest sample is evicted.
func (pb *PerformanceBenchmark) Record(duration time.Duration) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	// If we're at max capacity, remove the oldest sample (at index 0).
	if len(pb.samples) >= pb.maxSamples {
		// Shift all samples left by one.
		copy(pb.samples, pb.samples[1:])
		// Slice off the last (now duplicated) element.
		pb.samples = pb.samples[:len(pb.samples)-1]
	}

	// Add the new sample at the end.
	pb.samples = append(pb.samples, duration)
}

// GetStats calculates and returns statistics for all recorded samples.
// This method is thread-safe.
func (pb *PerformanceBenchmark) GetStats() map[string]interface{} {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	// Handle the case of no samples.
	if len(pb.samples) == 0 {
		return map[string]interface{}{
			"name":         pb.name,
			"sample_count": 0,
			"min_duration": "0s",
			"max_duration": "0s",
			"avg_duration": "0s",
		}
	}

	// Calculate min, max, and total.
	min := pb.samples[0]
	max := pb.samples[0]
	var total time.Duration

	for _, sample := range pb.samples {
		if sample < min {
			min = sample
		}
		if sample > max {
			max = sample
		}
		total += sample
	}

	// Calculate average.
	avg := total / time.Duration(len(pb.samples))

	return map[string]interface{}{
		"name":         pb.name,
		"sample_count": len(pb.samples),
		"min_duration": min.String(),
		"max_duration": max.String(),
		"avg_duration": avg.String(),
		"total_time":   total.String(),
	}
}

// LogStats is a helper to log the current stats of the benchmark using Info.
func (pb *PerformanceBenchmark) LogStats(ml *MessageLog, async bool) {
	stats := pb.GetStats()
	Info(ml, async, "Performance benchmark statistics",
		slog.Any("benchmark_stats", stats))
}

// TimedOperation wraps a function call to measure its duration.
// It returns the duration and any error from the operation.
func TimedOperation(name string, operation func() error) (time.Duration, error) {
	start := time.Now()
	err := operation()
	duration := time.Since(start)
	return duration, err
}

// TimedOperationWithLogging wraps a function call, measures its duration,
// and logs the start, completion, or failure automatically.
func TimedOperationWithLogging(ml *MessageLog, async bool, name string, operation func() error) error {
	start := time.Now()

	Info(ml, async, "Starting timed operation",
		slog.String("operation_name", name))

	// Execute the operation.
	err := operation()
	duration := time.Since(start)

	// Log the outcome.
	if err != nil {
		OperationError(ml, async, err, "Timed operation failed",
			slog.String("operation_name", name),
			slog.String("operation_duration", duration.String()))
	} else {
		Info(ml, async, "Timed operation completed",
			slog.String("operation_name", name),
			slog.String("operation_duration", duration.String()))
	}

	return err
}

// Global performance benchmark registry
var (
	// benchmarks stores all globally registered benchmarks by name.
	benchmarks = make(map[string]*PerformanceBenchmark)
	// benchMutex protects the global benchmarks map.
	benchMutex sync.RWMutex
)

// RegisterBenchmark creates and registers a global performance benchmark.
// It returns the newly created benchmark.
func RegisterBenchmark(name string, maxSamples int) *PerformanceBenchmark {
	benchMutex.Lock() // Use full lock to write to the map.
	defer benchMutex.Unlock()

	benchmark := NewPerformanceBenchmark(name, maxSamples)
	benchmarks[name] = benchmark
	return benchmark
}

// GetBenchmark retrieves a registered benchmark by name.
// It returns nil if the benchmark does not exist.
func GetBenchmark(name string) *PerformanceBenchmark {
	benchMutex.RLock() // Use read lock to read from the map.
	defer benchMutex.RUnlock()

	return benchmarks[name]
}

// RecordGlobalBenchmark records a duration to a globally registered benchmark.
// If the benchmark does not exist, this function does nothing.
func RecordGlobalBenchmark(name string, duration time.Duration) {
	if benchmark := GetBenchmark(name); benchmark != nil {
		benchmark.Record(duration)
	}
}

// LogAllBenchmarkStats logs the statistics for all registered benchmarks.
func LogAllBenchmarkStats(ml *MessageLog, async bool) {
	benchMutex.RLock() // Use read lock while iterating map keys.
	allStats := make(map[string]interface{})
	for name, benchmark := range benchmarks {
		// GetStats has its own internal lock.
		allStats[name] = benchmark.GetStats()
	}
	benchMutex.RUnlock()

	Info(ml, async, "All benchmark statistics",
		slog.Any("all_benchmarks", allStats))
}
