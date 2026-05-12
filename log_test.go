package logfile_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mkadit/logfile"
)

// Global mutex to prevent concurrent logger initialization
var (
	setupMutex   sync.Mutex
	loggerActive bool // Track if logger is currently active
)

// TestMain controls test execution, handling global setup and teardown.
func TestMain(m *testing.M) {
	// --- GLOBAL SETUP ---
	// We call CreateLogger ONCE here. This starts the worker pools.
	// Individual tests will only re-apply configuration using SetLogger.
	setupMutex.Lock()

	// Use a minimal default config for the initial creation
	config := logfile.LogConfiguration{
		General: logfile.GeneralConfig{
			LevelsByType: map[string]string{
				"message": "info", "event": "info", "error": "error",
				"critical": "critical", "http": "info", "debug": "debug", "index": "info",
			},
			DevelopmentMode:          false,
			PrettyPrint:              false,
			AddSource:                false,
			EnableCentralizedLogging: false,
			LogChannel:               20000,
			WorkerPoolSize:           8,
			MaxWorkerPoolSize:        16,
			EnableObjectPooling:      true,
		},
		Files: map[string]logfile.FileConfig{
			"message": {
				Path:          "test_logs/GLOBAL/message.log", // Generic path
				MinLevel:      "info",
				Structured:    true,
				SlogWriter:    true,
				UseFileWriter: false, // Disable file writing for tests
			},
		},
	}
	logfile.Config.Store(&config)
	logfile.CreateLogger()
	loggerActive = true
	setupMutex.Unlock()

	// --- RUN ALL TESTS ---
	exitCode := m.Run()

	// --- GLOBAL TEARDOWN ---
	// We call Shutdown ONCE here, after all tests are finished.
	setupMutex.Lock()
	if loggerActive {
		logfile.Shutdown()
		loggerActive = false
		time.Sleep(100 * time.Millisecond) // Give time for shutdown
	}
	// Clean up all test log directories
	os.RemoveAll("test_logs")
	os.RemoveAll("bench_logs")
	setupMutex.Unlock()

	os.Exit(exitCode)
}

// ============================================================================
// Test Data Structures
// ============================================================================

// Small struct for testing
type UserAction struct {
	UserID    string
	Action    string
	Timestamp time.Time
	Success   bool
}

// Medium complexity struct
type OrderData struct {
	OrderID     string
	UserID      string
	Items       []string
	TotalAmount float64
	Metadata    map[string]string
	CreatedAt   time.Time
}

// Large complex struct
type ComplexData struct {
	Users      []UserAction
	Metadata   map[string]interface{}
	Nested     []map[string]interface{}
	Timestamps []time.Time
	LargeText  string
}

// Very large struct with binary data
type LargeReport struct {
	Data     []byte
	Metadata map[string]string
	Summary  string
}

// ============================================================================
// Setup and Teardown
// ============================================================================

// setupTest configures the logger for a specific test.
// It NO LONGER calls CreateLogger or Shutdown.
func setupTest(t *testing.T) {
	setupMutex.Lock() // Lock to ensure tests run serially

	if !loggerActive {
		t.Fatal("Logger was not initialized by TestMain")
	}

	// Create a minimal test configuration
	config := logfile.LogConfiguration{
		General: logfile.GeneralConfig{
			LevelsByType: map[string]string{
				"message":  "info",
				"event":    "info",
				"error":    "error",
				"critical": "critical",
				"http":     "info",
				"debug":    "debug",
				"index":    "info",
			},
			DevelopmentMode:          false,
			PrettyPrint:              false,
			AddSource:                false,
			EnableCentralizedLogging: false,
			LogChannel:               20000,
			WorkerPoolSize:           4,
			MaxWorkerPoolSize:        8,
			EnableObjectPooling:      true,
		},
		Files: map[string]logfile.FileConfig{
			"message": {
				Path:          fmt.Sprintf("test_logs/%s/message.log", t.Name()),
				MinLevel:      "info",
				MaxSizeMB:     10,
				MaxBackups:    2,
				MaxAgeDays:    0,
				Compress:      false,
				Structured:    true,
				StdWriter:     false,
				SlogWriter:    true,
				ConsoleStd:    false,
				UseFileWriter: false, // Keep file writing disabled
				AdditionalOutputs: logfile.AdditionalOutputConfig{
					SlogOutputs: []logfile.OutputTarget{},
					StdOutputs:  []logfile.OutputTarget{},
				},
			},
		},
	}

	// Set the configuration and re-apply it to the active logger
	logfile.Config.Store(&config)
	if err := logfile.SetLogger(); err != nil {
		t.Fatalf("Failed to set logger config: %v", err)
	}
}

// teardownTest cleans up after a test.
// It NO LONGER calls Shutdown.
func teardownTest(t *testing.T) {
	// Clean up test-specific logs (if any were created)
	// We keep os.RemoveAll("test_logs") in TestMain as a final cleanup

	setupMutex.Unlock() // Unlock to allow the next test to run
}

// setupBenchmark configures the logger for a specific benchmark.
// It NO LONGER calls CreateLogger or Shutdown.
func setupBenchmark(b *testing.B) {
	setupMutex.Lock() // Lock to ensure benchmarks run serially

	if !loggerActive {
		b.Fatal("Logger was not initialized by TestMain")
	}

	config := logfile.LogConfiguration{
		General: logfile.GeneralConfig{
			LevelsByType: map[string]string{
				"message":  "info",
				"event":    "info",
				"error":    "error",
				"critical": "critical",
				"http":     "info",
				"debug":    "debug",
				"index":    "info",
			},
			DevelopmentMode:          false,
			PrettyPrint:              false,
			AddSource:                false,
			EnableCentralizedLogging: false,
			LogChannel:               20000,
			WorkerPoolSize:           8,
			MaxWorkerPoolSize:        16,
			EnableObjectPooling:      true,
		},
		Files: map[string]logfile.FileConfig{
			"message": {
				Path:          "bench_logs/message.log",
				MinLevel:      "info",
				Structured:    true,
				SlogWriter:    true,
				UseFileWriter: false, // Keep file writing disabled
			},
		},
	}

	// Set the configuration and re-apply it
	logfile.Config.Store(&config)
	if err := logfile.SetLogger(); err != nil {
		b.Fatalf("Failed to set logger config: %v", err)
	}

	b.ResetTimer()
}

// teardownBenchmark cleans up after a benchmark.
// It NO LONGER calls Shutdown.
func teardownBenchmark(b *testing.B) {
	b.StopTimer()
	// Global teardown is handled by TestMain
	setupMutex.Unlock() // Unlock to allow the next benchmark to run
}

// ============================================================================
// Helper Functions
// ============================================================================

func generateUserAction() UserAction {
	return UserAction{
		UserID:    "user_12345",
		Action:    "click_button",
		Timestamp: time.Now(),
		Success:   true,
	}
}

func generateOrderData() OrderData {
	return OrderData{
		OrderID:     "ORD-12345",
		UserID:      "user_12345",
		Items:       []string{"item1", "item2", "item3"},
		TotalAmount: 99.99,
		Metadata: map[string]string{
			"payment_method": "credit_card",
			"shipping":       "express",
		},
		CreatedAt: time.Now(),
	}
}

func generateComplexData(userCount, metadataKeys, nestedMaps int) ComplexData {
	users := make([]UserAction, userCount)
	for i := 0; i < userCount; i++ {
		users[i] = generateUserAction()
	}

	metadata := make(map[string]interface{}, metadataKeys)
	for i := 0; i < metadataKeys; i++ {
		metadata[fmt.Sprintf("key_%d", i)] = fmt.Sprintf("value_%d", i)
	}

	nested := make([]map[string]interface{}, nestedMaps)
	for i := 0; i < nestedMaps; i++ {
		nested[i] = map[string]interface{}{
			"id":    i,
			"name":  fmt.Sprintf("nested_%d", i),
			"value": i * 100,
		}
	}

	timestamps := make([]time.Time, userCount)
	for i := 0; i < userCount; i++ {
		timestamps[i] = time.Now().Add(time.Duration(i) * time.Second)
	}

	return ComplexData{
		Users:      users,
		Metadata:   metadata,
		Nested:     nested,
		Timestamps: timestamps,
		LargeText:  generateRandomString(1000),
	}
}

func generateLargeReport(sizeMB int) LargeReport {
	data := make([]byte, sizeMB*1024*1024)
	rand.Read(data)

	metadata := make(map[string]string, 1000)
	for i := 0; i < 1000; i++ {
		metadata[fmt.Sprintf("key_%d", i)] = fmt.Sprintf("value_%d", i)
	}

	return LargeReport{
		Data:     data,
		Metadata: metadata,
		Summary:  "Large report summary",
	}
}

func generateRandomString(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return base64.StdEncoding.EncodeToString(bytes)[:length]
}

// ============================================================================
// SECTION 1: Basic Functionality Tests
// ============================================================================

func TestBasicLogging(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	t.Run("Info", func(t *testing.T) {
		logfile.Info(nil, true, "Test info message",
			slog.String("test_key", "test_value"))
	})

	t.Run("Warn", func(t *testing.T) {
		logfile.Warn(nil, true, "Test warning message",
			slog.String("warning_type", "test"))
	})

	t.Run("Error", func(t *testing.T) {
		err := fmt.Errorf("test error")
		logfile.Error(nil, true, err, "Test error message",
			slog.String("error_context", "testing"))
	})

	t.Run("Critical", func(t *testing.T) {
		err := fmt.Errorf("critical error")
		logfile.Critical(nil, true, err, "Test critical message")
	})

	t.Run("HTTP", func(t *testing.T) {
		logfile.HTTP(nil, true, "HTTP request",
			slog.String("method", "GET"),
			slog.String("path", "/api/test"))
	})

	t.Run("Debug", func(t *testing.T) {
		logfile.Debug(nil, true, "Debug message",
			slog.String("debug_info", "test"))
	})

	// Give async logs time to process
	time.Sleep(100 * time.Millisecond)
}

func TestMessageLog(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	ml := logfile.CreateMessageLog(
		"TestAction",
		"TEST-123",
		"TestEntity",
		"TEST",
		"/test/url",
	)

	if ml.Action != "TestAction" {
		t.Errorf("Expected Action 'TestAction', got '%s'", ml.Action)
	}

	if ml.ReffTrx != "TEST-123" {
		t.Errorf("Expected ReffTrx 'TEST-123', got '%s'", ml.ReffTrx)
	}

	// Test duration tracking
	time.Sleep(10 * time.Millisecond)
	ml.RecordStepDuration()

	duration := ml.GetDurationSinceStart()
	if duration < 10*time.Millisecond {
		t.Errorf("Expected duration >= 10ms, got %v", duration)
	}
}

// ============================================================================
// SECTION 2: Small Test Cases - Simple Attributes
// ============================================================================

func TestSimpleAttributes(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name  string
		count int
	}{
		{"Small_100", 100},
		{"Medium_1000", 1000},
		{"Large_10000", 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			for i := 0; i < tt.count; i++ {
				logfile.Info(nil, true, "Simple log",
					slog.String("user_id", "12345"),
					slog.Int("count", i),
					slog.Bool("active", true),
					slog.Float64("score", 98.5))
			}
			duration := time.Since(start)

			throughput := float64(tt.count) / duration.Seconds()
			t.Logf("Logged %d messages in %v (%.0f logs/sec)",
				tt.count, duration, throughput)
		})
	}
}

func BenchmarkSimpleAttributes_Async(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, true, "Benchmark log",
			slog.String("user_id", "12345"),
			slog.Int("iteration", i),
			slog.Bool("active", true))
	}
}

func BenchmarkSimpleAttributes_Sync(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, false, "Benchmark log",
			slog.String("user_id", "12345"),
			slog.Int("iteration", i),
			slog.Bool("active", true))
	}
}

func BenchmarkSimpleAttributes_Parallel(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			logfile.Info(nil, true, "Parallel log",
				slog.String("user_id", "12345"),
				slog.Int("iteration", i))
			i++
		}
	})
}

// ============================================================================
// SECTION 3: Small Structs with slog.Any
// ============================================================================

func TestSmallStructs(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name  string
		count int
	}{
		{"Small_100", 100},
		{"Medium_1000", 1000},
		{"Large_10000", 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			for i := 0; i < tt.count; i++ {
				action := generateUserAction()
				logfile.Info(nil, true, "User action",
					slog.Any("action", action))
			}
			duration := time.Since(start)

			throughput := float64(tt.count) / duration.Seconds()
			t.Logf("Logged %d structs in %v (%.0f logs/sec)",
				tt.count, duration, throughput)
		})
	}
}

func BenchmarkSmallStruct_Async(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	action := generateUserAction()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, true, "User action", slog.Any("action", action))
	}
}

func BenchmarkSmallStruct_Sync(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	action := generateUserAction()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, false, "User action", slog.Any("action", action))
	}
}

func BenchmarkSmallStruct_vs_Decomposed(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	action := generateUserAction()

	b.Run("WithAny", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "User action", slog.Any("action", action))
		}
	})

	b.Run("Decomposed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "User action",
				slog.String("user_id", action.UserID),
				slog.String("action", action.Action),
				slog.Time("timestamp", action.Timestamp),
				slog.Bool("success", action.Success))
		}
	})
}

// ============================================================================
// SECTION 4: Medium Complexity Objects
// ============================================================================

func TestMediumComplexity(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name  string
		count int
	}{
		{"Small_10", 10},
		{"Medium_100", 100},
		{"Large_1000", 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			for i := 0; i < tt.count; i++ {
				order := generateOrderData()
				logfile.Info(nil, true, "Order processed",
					slog.Any("order", order))
			}
			duration := time.Since(start)

			throughput := float64(tt.count) / duration.Seconds()
			t.Logf("Logged %d orders in %v (%.0f logs/sec)",
				tt.count, duration, throughput)
		})
	}
}

func BenchmarkMediumComplexity(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	order := generateOrderData()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, true, "Order processed", slog.Any("order", order))
	}
}

// ============================================================================
// SECTION 5: Large Complex Objects
// ============================================================================

func TestLargeComplexObjects(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name         string
		count        int
		users        int
		metadataKeys int
		nestedMaps   int
	}{
		{"Small_10users", 10, 10, 10, 5},
		{"Medium_50users", 10, 50, 25, 10},
		{"Large_100users", 10, 100, 50, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := generateComplexData(tt.users, tt.metadataKeys, tt.nestedMaps)

			var memBefore runtime.MemStats
			runtime.ReadMemStats(&memBefore)

			start := time.Now()
			for i := 0; i < tt.count; i++ {
				logfile.Info(nil, true, "Complex data processed",
					slog.Any("data", data))
			}
			duration := time.Since(start)

			var memAfter runtime.MemStats
			runtime.ReadMemStats(&memAfter)

			throughput := float64(tt.count) / duration.Seconds()
			memUsed := memAfter.Alloc - memBefore.Alloc

			t.Logf("Logged %d complex objects in %v (%.0f logs/sec)",
				tt.count, duration, throughput)
			t.Logf("Memory used: %d MB", memUsed/(1024*1024))
			if tt.count > 0 {
				t.Logf("Avg per log: %d KB", memUsed/uint64(tt.count)/1024)
			}
		})
	}
}

func BenchmarkLargeComplexObject(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	data := generateComplexData(100, 50, 10)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, true, "Complex data", slog.Any("data", data))
	}
}

func BenchmarkLargeComplexObject_Summary(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	data := generateComplexData(100, 50, 10)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logfile.Info(nil, true, "Complex data processed",
			slog.Int("user_count", len(data.Users)),
			slog.Int("metadata_keys", len(data.Metadata)),
			slog.Int("nested_count", len(data.Nested)),
			slog.Int("text_length", len(data.LargeText)))
	}
}

// ============================================================================
// SECTION 6: Very Large Objects
// ============================================================================

func TestVeryLargeObjects(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large object test in short mode")
	}

	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name   string
		sizeMB int
		count  int
	}{
		{"1MB", 1, 10},
		{"5MB", 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := generateLargeReport(tt.sizeMB)

			var memBefore runtime.MemStats
			runtime.ReadMemStats(&memBefore)

			start := time.Now()
			for i := 0; i < tt.count; i++ {
				logfile.Info(nil, true, "Large report generated",
					slog.Any("report", report))
			}
			duration := time.Since(start)

			var memAfter runtime.MemStats
			runtime.ReadMemStats(&memAfter)

			throughput := float64(tt.count) / duration.Seconds()
			memUsed := memAfter.Alloc - memBefore.Alloc

			t.Logf("Logged %d reports (%dMB each) in %v (%.2f logs/sec)",
				tt.count, tt.sizeMB, duration, throughput)
			t.Logf("Memory used: %d MB", memUsed/(1024*1024))
			t.Logf("GC runs: %d", memAfter.NumGC-memBefore.NumGC)
		})
	}
}

func TestVeryLargeObject_Optimized(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	report := generateLargeReport(10)

	start := time.Now()
	for i := 0; i < 100; i++ {
		logfile.Info(nil, true, "Large report generated",
			slog.Int("size_bytes", len(report.Data)),
			slog.Int("size_mb", len(report.Data)/(1024*1024)),
			slog.Int("metadata_count", len(report.Metadata)),
			slog.String("summary", report.Summary))
	}
	duration := time.Since(start)

	throughput := float64(100) / duration.Seconds()
	t.Logf("Logged 100 report summaries in %v (%.0f logs/sec)",
		duration, throughput)
}

// ============================================================================
// SECTION 7: Operation Tracking
// ============================================================================

func TestOperationTracking(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name  string
		steps int
		count int
	}{
		{"FewSteps_3", 3, 100},
		{"ManySteps_10", 10, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()

			for i := 0; i < tt.count; i++ {
				ml := logfile.OperationStart(
					"TestOperation",
					fmt.Sprintf("TRX-%d", i),
					"TestService",
					"TEST",
					"/test/endpoint",
				)

				for step := 0; step < tt.steps; step++ {
					logfile.OperationStep(ml, true,
						fmt.Sprintf("Step%d", step),
						fmt.Sprintf("Executing step %d", step))
					time.Sleep(time.Microsecond)
				}

				logfile.OperationComplete(ml, true, "Operation completed")
			}

			duration := time.Since(start)
			totalLogs := tt.count * (tt.steps + 2)
			throughput := float64(totalLogs) / duration.Seconds()

			t.Logf("Tracked %d operations (%d steps each) in %v",
				tt.count, tt.steps, duration)
			t.Logf("Total logs: %d (%.0f logs/sec)", totalLogs, throughput)
		})
	}
}

func BenchmarkOperationTracking(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ml := logfile.OperationStart(
			"BenchmarkOp",
			fmt.Sprintf("TRX-%d", i),
			"BenchService",
			"BENCH",
			"/bench",
		)

		logfile.OperationStep(ml, true, "Step1", "First step")
		logfile.OperationStep(ml, true, "Step2", "Second step")
		logfile.OperationStep(ml, true, "Step3", "Third step")
		logfile.OperationComplete(ml, true, "Completed")
	}
}

// ============================================================================
// SECTION 8: High Traffic Scenarios
// ============================================================================

func TestHighTraffic_Sustained(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping high traffic test in short mode")
	}

	setupTest(t)
	defer teardownTest(t)

	duration := 5 * time.Second
	targetRate := 10000 // 10K logs/sec for testing

	var counter atomic.Int64

	start := time.Now()
	done := make(chan bool)
	finished := make(chan bool) // Add channel to wait for goroutine completion

	go func() {
		defer close(finished) // Signal when goroutine is done
		ticker := time.NewTicker(time.Second / time.Duration(targetRate))
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				logfile.Info(nil, true, "High traffic log",
					slog.Int64("counter", counter.Add(1)),
					slog.String("request_id", fmt.Sprintf("REQ-%d", counter.Load())))
			case <-done:
				return
			}
		}
	}()

	time.Sleep(duration)
	close(done)
	<-finished // Wait for goroutine to actually finish

	elapsed := time.Since(start)
	total := counter.Load()
	actualRate := float64(total) / elapsed.Seconds()

	t.Logf("Sustained traffic test:")
	t.Logf("  Duration: %v", elapsed)
	t.Logf("  Total logs: %d", total)
	t.Logf("  Target rate: %d logs/sec", targetRate)
	t.Logf("  Actual rate: %.0f logs/sec", actualRate)

	// Give time for any pending logs to be processed
	time.Sleep(200 * time.Millisecond)
}

func BenchmarkHighTraffic_Concurrent(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			logfile.Info(nil, true, "Concurrent log",
				slog.Int("worker_id", i%8),
				slog.Int("iteration", i))
			i++
		}
	})
}

// ============================================================================
// SECTION 9: Memory Impact Tests
// ============================================================================

func TestMemoryImpact(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	setupTest(t)
	defer teardownTest(t)

	tests := []struct {
		name    string
		logFunc func()
		count   int
	}{
		{
			name: "SimpleAttributes",
			logFunc: func() {
				logfile.Info(nil, true, "Simple",
					slog.String("key", "value"))
			},
			count: 10000,
		},
		{
			name: "SmallStruct",
			logFunc: func() {
				action := generateUserAction()
				logfile.Info(nil, true, "Struct", slog.Any("action", action))
			},
			count: 10000,
		},
		{
			name: "LargeObject",
			logFunc: func() {
				data := generateComplexData(100, 50, 10)
				logfile.Info(nil, true, "Large", slog.Any("data", data))
			},
			count: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC()
			time.Sleep(100 * time.Millisecond)

			var memBefore runtime.MemStats
			runtime.ReadMemStats(&memBefore)

			start := time.Now()
			for i := 0; i < tt.count; i++ {
				tt.logFunc()
			}
			duration := time.Since(start)

			time.Sleep(500 * time.Millisecond)
			runtime.GC()

			var memAfter runtime.MemStats
			runtime.ReadMemStats(&memAfter)

			allocDiff := memAfter.TotalAlloc - memBefore.TotalAlloc
			gcRuns := memAfter.NumGC - memBefore.NumGC

			t.Logf("Logged %d messages in %v", tt.count, duration)
			t.Logf("Total allocated: %d MB", allocDiff/(1024*1024))
			t.Logf("Avg per log: %d bytes", allocDiff/uint64(tt.count))
			t.Logf("GC runs: %d", gcRuns)
			t.Logf("Heap in use: %d MB", memAfter.HeapInuse/(1024*1024))
		})
	}
}

// ============================================================================
// SECTION 10: Comparative Benchmarks
// ============================================================================

func BenchmarkComparison_AsyncVsSync(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.Run("Async", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Message", slog.Int("i", i))
		}
	})

	b.Run("Sync", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, false, "Message", slog.Int("i", i))
		}
	})
}

func BenchmarkComparison_AttributeTypes(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.Run("String", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.String("key", "value"))
		}
	})

	b.Run("Int", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.Int("key", 12345))
		}
	})

	b.Run("Bool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.Bool("key", true))
		}
	})

	b.Run("Time", func(b *testing.B) {
		now := time.Now()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.Time("key", now))
		}
	})

	b.Run("SmallStruct", func(b *testing.B) {
		action := generateUserAction()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.Any("key", action))
		}
	})

	b.Run("Map10Keys", func(b *testing.B) {
		m := make(map[string]string, 10)
		for i := 0; i < 10; i++ {
			m[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.Any("key", m))
		}
	})

	b.Run("Slice100Items", func(b *testing.B) {
		s := make([]int, 100)
		for i := 0; i < 100; i++ {
			s[i] = i
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logfile.Info(nil, true, "Msg", slog.Any("key", s))
		}
	})
}

// ============================================================================
// SECTION 11: Stress Tests
// ============================================================================

func TestStress_ChannelSaturation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	setupTest(t)
	defer teardownTest(t)

	goroutines := 100
	logsPerGoroutine := 1000

	var wg sync.WaitGroup
	var counter atomic.Int64

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < logsPerGoroutine; j++ {
				logfile.Info(nil, true, "Stress test",
					slog.Int("goroutine", id),
					slog.Int("iteration", j),
					slog.Int64("counter", counter.Add(1)))
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	total := goroutines * logsPerGoroutine
	throughput := float64(total) / duration.Seconds()

	t.Logf("Stress test completed:")
	t.Logf("  Goroutines: %d", goroutines)
	t.Logf("  Logs per goroutine: %d", logsPerGoroutine)
	t.Logf("  Total logs: %d", total)
	t.Logf("  Duration: %v", duration)
	t.Logf("  Throughput: %.0f logs/sec", throughput)

	// Give time for all logs to be processed
	time.Sleep(200 * time.Millisecond)
}

// ============================================================================
// SECTION 12: Configuration Tests
// ============================================================================

func TestConfigurationLoading(t *testing.T) {
	// This test is now implicitly covered by TestMain and setupTest,
	// but we can add a specific check for LoadConfigurationFromFile

	// Create a temporary config file
	configData := map[string]interface{}{
		"general": map[string]interface{}{
			"levels_by_type": map[string]string{
				"message": "info",
			},
			"log_channel":      20000,
			"worker_pool_size": 8,
			"development_mode": false,
		},
		"files": map[string]interface{}{
			"message": map[string]interface{}{
				"path":            "test_logs/message.log",
				"min_level":       "info",
				"structured":      true,
				"slog_writer":     true,
				"use_file_writer": false,
			},
		},
	}

	configJSON, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	tmpFile := "test_config.json"
	if err := os.WriteFile(tmpFile, configJSON, 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}
	defer os.Remove(tmpFile)

	// Load configuration
	if err := logfile.LoadConfigurationFromFile(tmpFile); err != nil {
		t.Fatalf("Failed to load configuration: %v", err)
	}

	// Apply the loaded configuration
	if err := logfile.SetLogger(); err != nil {
		t.Fatalf("Failed to set loaded configuration: %v", err)
	}
}

// ============================================================================
// SECTION 13: Utility Benchmarks
// ============================================================================

func BenchmarkMemoryAllocation(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logfile.Info(nil, true, "Allocation test",
			slog.String("key1", "value1"),
			slog.Int("key2", 12345),
			slog.Bool("key3", true))
	}
}

func BenchmarkLatency_Percentiles(b *testing.B) {
	setupBenchmark(b)
	defer teardownBenchmark(b)

	latencies := make([]time.Duration, b.N)

	for i := 0; i < b.N; i++ {
		start := time.Now()
		logfile.Info(nil, true, "Latency test", slog.Int("i", i))
		latencies[i] = time.Since(start)
	}

	p50 := percentile(latencies, 0.50)
	p95 := percentile(latencies, 0.95)
	p99 := percentile(latencies, 0.99)
	p999 := percentile(latencies, 0.999)

	b.ReportMetric(float64(p50.Nanoseconds()), "p50-ns")
	b.ReportMetric(float64(p95.Nanoseconds()), "p95-ns")
	b.ReportMetric(float64(p99.Nanoseconds()), "p99-ns")
	b.ReportMetric(float64(p999.Nanoseconds()), "p999-ns")
}

func percentile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}

	index := int(math.Ceil(float64(len(durations)) * p))
	if index >= len(durations) {
		index = len(durations) - 1
	}

	return durations[index]
}

// ============================================================================
// SECTION 12b: Converter Tests — []byte readability in standard logger
// ============================================================================

// KVType mimics the data structs the user logs (Key+Value pattern)
type KVType struct {
	Key   string
	Value any
}

// ISOStruct mimics an ISO 8583 message with nested fields
type ISOStruct struct {
	FullMessage string
	MTI         string
	Fields      map[string]string
}

func TestFormatValueForStdLogger(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		checkFn func(t *testing.T, result string)
	}{
		{
			name:  "flat_byte_slice",
			input: []byte("0200"),
			checkFn: func(t *testing.T, r string) {
				if r != "0200" {
					t.Errorf("expected '0200', got '%s'", r)
				}
			},
		},
		{
			name:  "basic_type_string",
			input: "hello",
			checkFn: func(t *testing.T, r string) {
				if r != "hello" {
					t.Errorf("expected 'hello', got '%s'", r)
				}
			},
		},
		{
			name:  "nil_interface",
			input: nil,
			checkFn: func(t *testing.T, r string) {
				// Should not panic; %+v on nil shows "<nil>"
				if r != "<nil>" {
					t.Errorf("expected '<nil>', got '%s'", r)
				}
			},
		},
		{
			name: "slice_of_kv_structs",
			input: []KVType{
				{Key: "header", Value: map[string]any{"Authorization": "Bearer token123"}},
				{Key: "body", Value: map[string]any{"success": true, "code": 200}},
			},
			checkFn: func(t *testing.T, r string) {
				// Should contain the key names and values
				if !strings.Contains(r, "header") {
					t.Errorf("result should contain 'header', got: %s", r)
				}
				if !strings.Contains(r, "Bearer token123") {
					t.Errorf("result should contain 'Bearer token123', got: %s", r)
				}
				if !strings.Contains(r, "body") {
					t.Errorf("result should contain 'body', got: %s", r)
				}
				if !strings.Contains(r, "success") {
					t.Errorf("result should contain 'success', got: %s", r)
				}
				// Should NOT contain []byte format
				if strings.Contains(r, "[66 101 97 114 101 114") {
					t.Errorf("result should not contain byte slice format, got: %s", r)
				}
			},
		},
		{
			name: "iso_struct",
			input: ISOStruct{
				FullMessage: "0200F23E440128E190000000000000001600",
				MTI:         "0200",
				Fields: map[string]string{
					"2":  "6015921000651304",
					"3":  "401000",
					"48": "WELI HANDAYANI HARRYANTO KURNIAWAN",
				},
			},
			checkFn: func(t *testing.T, r string) {
				if !strings.Contains(r, "FullMessage") {
					t.Errorf("result should contain 'FullMessage', got: %s", r)
				}
				if !strings.Contains(r, "MTI") {
					t.Errorf("result should contain 'MTI', got: %s", r)
				}
				if !strings.Contains(r, "6015921000651304") {
					t.Errorf("result should contain '6015921000651304', got: %s", r)
				}
			},
		},
		{
			name:  "nested_maps_in_interface",
			input: map[string]any{"outer": map[string]any{"inner": "value"}},
			checkFn: func(t *testing.T, r string) {
				if !strings.Contains(r, "outer") || !strings.Contains(r, "inner") || !strings.Contains(r, "value") {
					t.Errorf("result should contain all nested keys/values, got: %s", r)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := logfile.FormatValueForStdLogger(tt.input)
			tt.checkFn(t, result)
		})
	}
}

// ============================================================================
// SECTION 12c: Integration Test — Converter through full log flow
// ============================================================================

func TestLogFlowWithComplexAttrs(t *testing.T) {
	setupTest(t)
	defer teardownTest(t)

	// Use sync logging to avoid async timing issues in test
	// This tests the full flow: logToSpecificLogger → formatStandardLog
	// with complex nested structs containing maps and slices

	t.Run("sync_kv_structs", func(t *testing.T) {
		data := []KVType{
			{Key: "header", Value: map[string]any{"Authorization": "Bearer tok123", "Content-Type": "application/json"}},
			{Key: "body", Value: map[string]any{"success": true, "code": 200, "message": "OK"}},
		}

		// Should not panic and should render maps readably
		logfile.Info(nil, false, "test kv data", slog.Any("data", data))
	})

	t.Run("sync_iso_struct", func(t *testing.T) {
		iso := ISOStruct{
			FullMessage: "0200F23E440128E190000000000000001600",
			MTI:         "0200",
			Fields: map[string]string{
				"2":  "6015921000651304",
				"3":  "401000",
				"48": "WELI HANDAYANI HARRYANTO KURNIAWAN",
			},
		}

		logfile.Info(nil, false, "test iso data", slog.Any("iso", iso))
	})

	t.Run("sync_deeply_nested", func(t *testing.T) {
		type Inner struct {
			Name    string
			Tags    map[string]string
			Payload []byte
		}
		type Outer struct {
			Code   string
			Inner  Inner
			Values map[string]any
		}

		data := Outer{
			Code: "REQ001",
			Inner: Inner{
				Name:    "test",
				Tags:    map[string]string{"env": "prod", "region": "us"},
				Payload: []byte("hello world"),
			},
			Values: map[string]any{
				"count": 42,
				"nested": map[string]any{
					"key":  "val",
					"data": []byte("nested bytes"),
				},
			},
		}

		logfile.Info(nil, false, "test deeply nested", slog.Any("data", data))
	})

	t.Run("async_fills_after_dispatch", func(t *testing.T) {
		// Simulate the pattern: data is initially sparse, then filled after dispatch
		type RequestData struct {
			Header map[string]string
			Body   map[string]string
		}

		req := &RequestData{}
		// Fill header AFTER creating the object
		req.Header = map[string]string{"Authorization": "Bearer tok"}

		// deepCopyAny now copies the pointer via reflection
		logfile.Info(nil, true, "async request with partial data", slog.Any("req", req))

		// Fill body AFTER async dispatch (simulates real-world timing)
		req.Body = map[string]string{"key": "value"}
		time.Sleep(100 * time.Millisecond)
		// At this point the async worker may have processed before or after the fill
	})

	t.Run("nil_interface_field", func(t *testing.T) {
		type WithAny struct {
			Name string
			Data any
		}

		// Data is nil — our converter must handle nil interface
		logfile.Info(nil, false, "nil interface field", slog.Any("item", WithAny{Name: "test"}))
	})

	t.Run("slog_group_attrs", func(t *testing.T) {
		// slog.Group creates slog.Value with no exported fields.
		// Our converter must unwrap via .Any() to see the actual values.
		logfile.Info(nil, false, "server started",
			slog.Group("data",
				slog.String("service", "middleware"),
				slog.Int("port", 8083),
				slog.Duration("read_timeout", 0),
				slog.Duration("keep_alive_period", 0),
			))
	})
}

func TestRuntimeMetricsCollection(t *testing.T) {
	// Test with metrics enabled
	config := logfile.TestLogConfiguration()
	config.General.AddMetrics = true
	config.General.MetricsInterval = 5 // 5 seconds for testing

	logfile.LoadConfigurationFromStruct(&config)
	defer func() {
		// Restore original config
		originalConfig := logfile.TestLogConfiguration()
		logfile.LoadConfigurationFromStruct(&originalConfig)
	}()

	// Test manual metrics collection
	metrics := logfile.GetRuntimeMetrics()
	if metrics == nil {
		t.Fatal("Expected runtime metrics, got nil")
	}

	// Verify basic metrics are populated
	if metrics.HeapAlloc == 0 {
		t.Error("Expected HeapAlloc to be non-zero")
	}

	if metrics.Goroutines == 0 {
		t.Error("Expected Goroutines to be non-zero")
	}

	if metrics.CollectionInterval != 5 {
		t.Errorf("Expected CollectionInterval to be 5, got %d", metrics.CollectionInterval)
	}

	// Test manual metrics logging
	logfile.LogRuntimeMetrics(nil, true)

	// Test configuration retrieval
	metricsConfig := logfile.GetMetricsConfig()
	if metricsConfig == nil {
		t.Fatal("Expected metrics config, got nil")
	}

	if !metricsConfig.AddMetrics {
		t.Error("Expected AddMetrics to be true")
	}

	// Test detailed metrics
	config.General.DetailedMetrics = true
	logfile.LoadConfigurationFromStruct(&config)

	metrics = logfile.GetRuntimeMetrics()
	if metrics.HeapSys == 0 {
		t.Error("Expected HeapSys to be non-zero when detailed metrics enabled")
	}

	// Test CPU metrics
	config.General.CPUMetrics = true
	logfile.LoadConfigurationFromStruct(&config)

	metrics = logfile.GetRuntimeMetrics()
	// CPU metrics might be 0 on non-Linux systems or if /proc/loadavg is not accessible
	// so we just verify the function doesn't panic
}

func TestRuntimeMetricsDisabled(t *testing.T) {
	// Test with metrics disabled
	config := logfile.TestLogConfiguration()
	config.General.AddMetrics = false

	logfile.LoadConfigurationFromStruct(&config)
	defer func() {
		// Restore original config
		originalConfig := logfile.TestLogConfiguration()
		logfile.LoadConfigurationFromStruct(&originalConfig)
	}()

	// Metrics should still be collectible manually, but background collector won't run
	metrics := logfile.GetRuntimeMetrics()
	if metrics == nil {
		t.Fatal("Expected runtime metrics, got nil")
	}

	// Background collection should not be active
	time.Sleep(1 * time.Second)
	lastMetrics := logfile.GetLastRuntimeMetrics()
	// Should be nil since background collector is disabled
	if lastMetrics != nil {
		t.Error("Expected last metrics to be nil when disabled")
	}
}

func BenchmarkMetricsCollection(b *testing.B) {
	config := logfile.TestLogConfiguration()
	config.General.AddMetrics = true
	logfile.LoadConfigurationFromStruct(&config)
	defer func() {
		originalConfig := logfile.TestLogConfiguration()
		logfile.LoadConfigurationFromStruct(&originalConfig)
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics := logfile.GetRuntimeMetrics()
		if metrics == nil {
			b.Fatal("Expected runtime metrics, got nil")
		}
	}
}
