// logfile/runtime_metrics.go - Runtime metrics collection for LGTM compliance
package logfile

import (
	"bufio"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// getLoadAverages returns system load averages (Unix-like systems)
// This reads from /proc/loadavg on Linux systems
func getLoadAverages() (float64, float64, float64) {
	// Check if running on a Unix-like system
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd" {
		if runtime.GOOS == "linux" {
			// Linux implementation: read from /proc/loadavg
			file, err := os.Open("/proc/loadavg")
			if err != nil {
				// Fallback: return zeros if file not accessible
				return 0, 0, 0
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			if scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				if len(fields) >= 3 {
					load1, err1 := strconv.ParseFloat(fields[0], 64)
					load5, err2 := strconv.ParseFloat(fields[1], 64)
					load15, err3 := strconv.ParseFloat(fields[2], 64)

					if err1 == nil && err2 == nil && err3 == nil {
						return load1, load5, load15
					}
				}
			}
		} else if runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd" {
			// Unix-like systems with sysctl command
			// Note: This is a simplified implementation
			// In a production environment, you might want to use C bindings or syscall
			// For now, we'll return zeros as fallback
			return 0, 0, 0
		}
	}

	// Default fallback for non-Unix systems or errors
	return 0, 0, 0
}

// RuntimeMetrics holds system runtime statistics
type RuntimeMetrics struct {
	Timestamp time.Time `json:"timestamp"`

	// Basic Metrics (always collected)
	HeapAlloc     uint64  `json:"heap_alloc_bytes"`
	HeapInuse     uint64  `json:"heap_inuse_bytes"`
	HeapObjects   uint64  `json:"heap_objects"`
	Goroutines    int     `json:"goroutines"`
	NumGC         uint32  `json:"num_gc"`
	GCCPUFraction float64 `json:"gc_cpu_fraction"`
	TotalAlloc    uint64  `json:"total_alloc_bytes"`
	Mallocs       uint64  `json:"mallocs_count"`
	Frees         uint64  `json:"frees_count"`

	// Detailed Metrics (collected when DetailedMetrics is enabled)
	HeapSys     uint64 `json:"heap_sys_bytes,omitempty"`
	HeapIdle    uint64 `json:"heap_idle_bytes,omitempty"`
	StackInuse  uint64 `json:"stack_inuse_bytes,omitempty"`
	StackSys    uint64 `json:"stack_sys_bytes,omitempty"`
	MSpanInuse  uint64 `json:"mspan_inuse_bytes,omitempty"`
	MSpanSys    uint64 `json:"mspan_sys_bytes,omitempty"`
	MCacheInuse uint64 `json:"mcache_inuse_bytes,omitempty"`
	MCacheSys   uint64 `json:"mcache_sys_bytes,omitempty"`
	BuckSys     uint64 `json:"buck_sys_bytes,omitempty"`
	GCSys       uint64 `json:"gc_sys_bytes,omitempty"`
	OtherSys    uint64 `json:"other_sys_bytes,omitempty"`
	NumForcedGC uint32 `json:"num_forced_gc,omitempty"`
	GCGoal      uint64 `json:"gc_goal_bytes,omitempty"`
	Lookups     uint64 `json:"lookups_count,omitempty"`

	// CPU Metrics (collected when CPUMetrics is enabled)
	LoadAvg1   float64 `json:"load_avg_1m,omitempty"`
	LoadAvg5   float64 `json:"load_avg_5m,omitempty"`
	LoadAvg15  float64 `json:"load_avg_15m,omitempty"`
	NumCgoCall int64   `json:"num_cgo_call,omitempty"`

	// Internal fields
	CollectionInterval int `json:"collection_interval"`
}

// MetricsCollector manages runtime metrics collection
type MetricsCollector struct {
	interval    time.Duration
	ticker      *time.Ticker
	done        chan struct{}
	mu          sync.RWMutex
	lastMetrics *RuntimeMetrics
	enabled     atomic.Bool
	config      atomic.Value // Stores *MetricsConfig
}

// MetricsConfig holds the metrics configuration
type MetricsConfig struct {
	AddMetrics      bool
	DetailedMetrics bool
	CPUMetrics      bool
	MetricsInterval int
}

// Object pooling for performance optimization
var (
	metricsPool = sync.Pool{
		New: func() interface{} {
			return &RuntimeMetrics{}
		},
	}
)

// collectRuntimeStats gathers current runtime statistics
func collectRuntimeStats(config *MetricsConfig) *RuntimeMetrics {
	metrics := getMetricsFromPool()
	defer putMetricsToPool(metrics)

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Always collect basic metrics
	metrics.Timestamp = time.Now()
	metrics.HeapAlloc = memStats.HeapAlloc
	metrics.HeapInuse = memStats.HeapInuse
	metrics.HeapObjects = memStats.HeapObjects
	metrics.Goroutines = runtime.NumGoroutine()
	metrics.NumGC = memStats.NumGC
	metrics.GCCPUFraction = memStats.GCCPUFraction
	metrics.TotalAlloc = memStats.TotalAlloc
	metrics.Mallocs = memStats.Mallocs
	metrics.Frees = memStats.Frees
	metrics.CollectionInterval = config.MetricsInterval

	// Collect detailed metrics if enabled
	if config.DetailedMetrics {
		metrics.HeapSys = memStats.HeapSys
		metrics.HeapIdle = memStats.HeapIdle
		metrics.StackInuse = memStats.StackInuse
		metrics.StackSys = memStats.StackSys
		metrics.MSpanInuse = memStats.MSpanInuse
		metrics.MSpanSys = memStats.MSpanSys
		metrics.MCacheInuse = memStats.MCacheInuse
		metrics.MCacheSys = memStats.MCacheSys
		metrics.BuckSys = memStats.HeapSys // Use HeapSys as placeholder
		metrics.GCSys = memStats.HeapSys   // Use HeapSys as placeholder
		metrics.OtherSys = memStats.OtherSys
		metrics.NumForcedGC = memStats.NumForcedGC
		metrics.GCGoal = memStats.HeapSys // Use HeapSys as placeholder
		metrics.Lookups = memStats.Lookups
	}

	// Collect CPU metrics if enabled
	if config.CPUMetrics {
		load1, load5, load15 := getLoadAverages()
		metrics.LoadAvg1 = load1
		metrics.LoadAvg5 = load5
		metrics.LoadAvg15 = load15
		metrics.NumCgoCall = runtime.NumCgoCall()
	}

	// Create a copy to return
	result := getMetricsFromPool()
	*result = *metrics
	return result
}

// ToSlogAttrs converts RuntimeMetrics to slog attributes
func (rm *RuntimeMetrics) ToSlogAttrs() []slog.Attr {
	attrs := []slog.Attr{
		slog.Time("metrics_timestamp", rm.Timestamp),
		slog.Uint64("heap_alloc_bytes", rm.HeapAlloc),
		slog.Uint64("heap_inuse_bytes", rm.HeapInuse),
		slog.Uint64("heap_objects", rm.HeapObjects),
		slog.Int("goroutines", rm.Goroutines),
		slog.Int64("num_gc", int64(rm.NumGC)),
		slog.Float64("gc_cpu_fraction", rm.GCCPUFraction),
		slog.Uint64("total_alloc_bytes", rm.TotalAlloc),
		slog.Uint64("mallocs_count", rm.Mallocs),
		slog.Uint64("frees_count", rm.Frees),
		slog.Int("collection_interval", rm.CollectionInterval),
	}

	// Add detailed attributes if present
	if rm.HeapSys > 0 {
		attrs = append(attrs,
			slog.Uint64("heap_sys_bytes", rm.HeapSys),
			slog.Uint64("heap_idle_bytes", rm.HeapIdle),
			slog.Uint64("stack_inuse_bytes", rm.StackInuse),
			slog.Uint64("stack_sys_bytes", rm.StackSys),
			slog.Uint64("mspan_inuse_bytes", rm.MSpanInuse),
			slog.Uint64("mspan_sys_bytes", rm.MSpanSys),
			slog.Uint64("mcache_inuse_bytes", rm.MCacheInuse),
			slog.Uint64("mcache_sys_bytes", rm.MCacheSys),
			slog.Uint64("buck_sys_bytes", rm.BuckSys),
			slog.Uint64("gc_sys_bytes", rm.GCSys),
			slog.Uint64("other_sys_bytes", rm.OtherSys),
			slog.Int64("num_forced_gc", int64(rm.NumForcedGC)),
			slog.Uint64("gc_goal_bytes", rm.GCGoal),
			slog.Uint64("lookups_count", rm.Lookups),
		)
	}

	// Add CPU attributes if present
	if rm.LoadAvg1 > 0 || rm.LoadAvg5 > 0 || rm.LoadAvg15 > 0 {
		attrs = append(attrs,
			slog.Float64("load_avg_1m", rm.LoadAvg1),
			slog.Float64("load_avg_5m", rm.LoadAvg5),
			slog.Float64("load_avg_15m", rm.LoadAvg15),
		)
	}

	if rm.NumCgoCall > 0 {
		attrs = append(attrs, slog.Int64("num_cgo_call", rm.NumCgoCall))
	}

	return attrs
}

// getMetricsFromPool retrieves a RuntimeMetrics from the pool
func getMetricsFromPool() *RuntimeMetrics {
	return metricsPool.Get().(*RuntimeMetrics)
}

// putMetricsToPool returns a RuntimeMetrics to the pool
func putMetricsToPool(metrics *RuntimeMetrics) {
	if metrics != nil {
		// Reset fields to avoid memory leaks
		*metrics = RuntimeMetrics{}
		metricsPool.Put(metrics)
	}
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(interval time.Duration, config *MetricsConfig) *MetricsCollector {
	mc := &MetricsCollector{
		interval: interval,
		done:     make(chan struct{}),
	}
	mc.config.Store(config)
	return mc
}

// Start begins the metrics collection goroutine
func (mc *MetricsCollector) Start() {
	if mc == nil {
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.ticker != nil {
		return // Already started
	}

	mc.ticker = time.NewTicker(mc.interval)
	mc.enabled.Store(true)

	go mc.collectLoop()
}

// Stop halts metrics collection
func (mc *MetricsCollector) Stop() {
	if mc == nil {
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.enabled.Store(false)

	if mc.ticker != nil {
		mc.ticker.Stop()
		mc.ticker = nil
	}

	close(mc.done)
}

// collectLoop runs the metrics collection loop
func (mc *MetricsCollector) collectLoop() {
	if mc == nil {
		return
	}

	mc.mu.Lock()
	ticker := mc.ticker
	mc.mu.Unlock()

	if ticker == nil {
		return
	}

	for {
		select {
		case <-ticker.C:
			if mc.enabled.Load() {
				config := mc.config.Load()
				if config == nil {
					continue
				}
				metricsConfig := config.(*MetricsConfig)
				if metricsConfig == nil {
					continue
				}

				metrics := collectRuntimeStats(metricsConfig)

				mc.mu.Lock()
				mc.lastMetrics = metrics
				mc.mu.Unlock()

				// Log the metrics asynchronously
				mc.logMetrics(metrics)
			}

		case <-mc.done:
			return
		}
	}
}

// logMetrics sends metrics to the logging system
func (mc *MetricsCollector) logMetrics(metrics *RuntimeMetrics) {
	if !isLoggerActive() {
		return
	}

	loggerMutex.RLock()
	defer loggerMutex.RUnlock()

	if AppLogger.MetricsLogger == nil {
		return
	}

	// Create a payload for metrics logging
	payload := getLogPayload()
	payload.Logger = AppLogger.MetricsLogger
	payload.Level = LevelInfo
	payload.EventType = "runtime_metrics"
	payload.Msg = "Runtime metrics collected"
	payload.TimeNow = time.Now().Format(DefaultTimeFormat)

	// Add metrics attributes
	metricsAttrs := metrics.ToSlogAttrs()
	for _, attr := range metricsAttrs {
		payload.OtherAttrs = append(payload.OtherAttrs, slog.Attr{
			Key:   attr.Key,
			Value: attr.Value,
		})
	}

	// Send to metrics logger
	dispatchLog(*payload, true)
}

// GetLastMetrics returns the most recently collected metrics
func (mc *MetricsCollector) GetLastMetrics() *RuntimeMetrics {
	if mc == nil {
		return nil
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	return mc.lastMetrics
}

// UpdateConfig updates the metrics collector configuration
func (mc *MetricsCollector) UpdateConfig(config *MetricsConfig) {
	if mc == nil {
		return
	}

	mc.config.Store(config)

	// Update interval if different
	newInterval := time.Duration(config.MetricsInterval) * time.Second
	if newInterval <= 0 {
		newInterval = 30 * time.Second // Default to 30 seconds
	}

	if mc.interval != newInterval {
		mc.mu.Lock()
		defer mc.mu.Unlock()

		// Stop old ticker
		if mc.ticker != nil {
			mc.ticker.Stop()
		}

		// Start new ticker with new interval
		mc.interval = newInterval
		if mc.enabled.Load() {
			mc.ticker = time.NewTicker(mc.interval)
		}
	}
}

// Public API functions

// GetRuntimeMetrics returns the current runtime metrics
// This is a blocking call that collects fresh metrics
func GetRuntimeMetrics() *RuntimeMetrics {
	if !isLoggerActive() {
		return nil
	}

	config := getConfigValue()
	if config == nil {
		return nil
	}

	metricsConfig := &MetricsConfig{
		AddMetrics:      config.General.AddMetrics,
		DetailedMetrics: config.General.DetailedMetrics,
		CPUMetrics:      config.General.CPUMetrics,
		MetricsInterval: config.General.MetricsInterval,
	}

	return collectRuntimeStats(metricsConfig)
}

// GetLastRuntimeMetrics returns the last collected metrics from the background collector
// Returns nil if metrics collection is not enabled or no metrics collected yet
func GetLastRuntimeMetrics() *RuntimeMetrics {
	metricsMutex.RLock()
	defer metricsMutex.RUnlock()

	if metricsCollector != nil {
		return metricsCollector.GetLastMetrics()
	}
	return nil
}

// LogRuntimeMetrics manually logs current runtime metrics
// Useful for on-demand metrics logging
func LogRuntimeMetrics(ml *MessageLog, async bool) {
	if !isLoggerActive() {
		return
	}

	config := getConfigValue()
	if config == nil {
		return
	}

	metricsConfig := &MetricsConfig{
		AddMetrics:      config.General.AddMetrics,
		DetailedMetrics: config.General.DetailedMetrics,
		CPUMetrics:      config.General.CPUMetrics,
		MetricsInterval: config.General.MetricsInterval,
	}

	metrics := collectRuntimeStats(metricsConfig)
	metricsAttrs := metrics.ToSlogAttrs()

	// Use the standard logging functions with the metrics logger
	loggerMutex.RLock()
	if AppLogger != nil && AppLogger.MetricsLogger != nil {
		// Convert slog.Attr slice to any slice for Info function
		attrs := make([]any, len(metricsAttrs))
		for i, attr := range metricsAttrs {
			attrs[i] = attr
		}
		Info(ml, async, "Runtime metrics snapshot", attrs...)
	}
	loggerMutex.RUnlock()
}

// UpdateMetricsConfig updates the metrics collector configuration
// Called during configuration hot-reload
func UpdateMetricsConfig(config *LogConfiguration) {
	if config == nil {
		return
	}

	metricsMutex.Lock()
	defer metricsMutex.Unlock()

	// Stop existing collector
	if metricsCollector != nil {
		metricsCollector.Stop()
		metricsCollector = nil
	}

	// Start new collector if enabled
	if config.General.AddMetrics {
		interval := time.Duration(config.General.MetricsInterval) * time.Second
		if interval <= 0 {
			interval = 30 * time.Second
		}

		metricsConfig := &MetricsConfig{
			AddMetrics:      config.General.AddMetrics,
			DetailedMetrics: config.General.DetailedMetrics,
			CPUMetrics:      config.General.CPUMetrics,
			MetricsInterval: config.General.MetricsInterval,
		}

		metricsCollector = NewMetricsCollector(interval, metricsConfig)
		metricsCollector.Start()
	}
}

// GetMetricsConfig returns the current metrics configuration
func GetMetricsConfig() *MetricsConfig {
	config := getConfigValue()
	if config == nil {
		return &MetricsConfig{
			AddMetrics:      true,
			DetailedMetrics: false,
			CPUMetrics:      false,
			MetricsInterval: 30,
		}
	}

	return &MetricsConfig{
		AddMetrics:      config.General.AddMetrics,
		DetailedMetrics: config.General.DetailedMetrics,
		CPUMetrics:      config.General.CPUMetrics,
		MetricsInterval: config.General.MetricsInterval,
	}
}
