# **Logfile: High-Performance Go Logging Package**

## **Overview**

**logfile** is a robust, high-performance logging package for Go applications.
It is designed for production environments, offering asynchronous logging, structured (JSON) and standard text formats, and granular configuration for multiple log outputs.

Do note that the performance may be lacking ～(　TロT)σ

### **Key Features**

* **Asynchronous Logging:**
  A channel-based worker pool processes log messages, minimizing impact on your application's request-handling goroutines.
* **Multi-Logger System:**
  Configure different loggers (e.g., `error`, `message`, `http`, `debug`) with their own files, log levels, and formats.
* **Structured & Text Logging:**
  Natively supports both `log/slog` for structured JSON logging and the standard `log` package for plain text.
* **Rich Configuration:**
  Configure everything via a JSON file, including log rotation, file paths, min levels, console output, and log forwarding.
* **Operation Tracing:**
  Built-in support for tracing an operation's lifecycle (`OperationStart`, `OperationStep`, `OperationComplete`) with a `MessageLog` context.
* **Performance Tracking:**
  Utilities like `DurationTracker` and `PerformanceBenchmark` to measure and report on code execution time.
* **Automatic Stack Traces:**
  Automatically capture and log full stack traces for errors.

---

## **Quick Start**

### 0. Install

```
go get github.com/mkadit/logfile
```

### **1. Initialization & Shutdown**

Initialize the logger when your application starts.
It's recommended to use the default configuration or load one from a file.
Call `Shutdown()` using `defer` to ensure all log messages are flushed before the application exits.

```go
package main

import (
 "log"
 "github.com/mkadit/logfile" // Import your logger package
)

func main() {
 // Initialize the logger. This loads the default config,
 // starts the worker pool, and sets up file handlers.
 logfile.CreateLogger()

 // Ensure all buffered logs are flushed on exit
 defer logfile.Shutdown()

 // --- Your application logic starts here ---
 log.Println("Application starting...")

 // Use the logger
 logfile.Info(nil, true, "Service started successfully")

 // ...
}
```

---

### **2. Configuration**

The logger can be configured using a JSON file (e.g., `config.json`).
If no file is loaded via `LoadConfigurationFromFile(path)`, `CreateLogger()` will use the `DefaultLogConfiguration()`.

#### **Example `config.json`:**

```json
{
  "general": {
    "development_mode": true,
    "pretty_print": true,
    "add_source": true,
    "enable_centralized_logging": true,
    "log_channel": 1000000,
    "worker_pool_size": 8,
    "max_worker_pool_size": 32,
    "enable_object_pooling": true
  },
  "files": {
    "message": {
      "path": "logs/Message/Message.log",
      "min_level": "info",
      "max_size_mb": 50,
      "max_backups": 3,
      "max_age_days": 7,
      "compress": true,
      "std_writer": true,
      "console_std": true,
      "use_file_writer": true
    },
    "error": {
      "path": "logs/Error/Error.log",
      "min_level": "debug",
      "std_writer": true,
      "console_std": true,
      "use_file_writer": true,
      "additional_outputs": {
        "std_outputs": [
          { "type": "message" }
        ]
      }
    },
    "debug": {
      "path": "logs/Debug/Debug.log",
      "min_level": "debug",
      "slog_writer": true,
      "use_file_writer": true
    }
  }
}
```

To load this configuration:

```go
err := logfile.LoadConfigurationFromFile("config.json")
if err != nil {
 log.Fatalf("Failed to load log config: %v", err)
}
logfile.CreateLogger()
defer logfile.Shutdown()
```

---

## **Basic Logging**

All standard logging functions share a common signature:

```go
func(ml *MessageLog, async bool, ...)
```

| Parameter        | Description                                                                                                               |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `ml *MessageLog` | The operation context. Pass `nil` for simple log messages.                                                                |
| `async bool`     | `true`: send log asynchronously (non-blocking, recommended). <br> `false`: write synchronously for critical/fatal errors. |

---

### **Info, Warn, Debug**

```go
import "log/slog"

// Simple info log (async)
logfile.Info(nil, true, "User logged in",
 slog.String("username", "admin"),
 slog.Int("user_id", 123),
)

// Warning (async)
logfile.Warn(nil, true, "Cache miss for key", slog.String("key", "user:profile:123"))

// Debug log (async)
// Note: Debug logs only write if "development_mode" is true in config
logfile.Debug(nil, true, "Full request body", slog.String("body", "..."))
```

---

### **Error Logging**

Error logs automatically capture stack traces.

```go
import "errors"

err := errors.New("something went wrong")

// Wrap the error to capture a stack trace
errWithStack := logfile.WithStack(err)

// Log the error (async)
// The stack trace will be included in the log output.
logfile.Error(nil, true, errWithStack, "Failed to process user request",
 slog.Int("user_id", 123),
)
```

---

### **Critical & Fatal**

* **Critical:** Logs a severe error (recommended synchronous).
* **Fatal:** Logs a severe error **and terminates the application** (`os.Exit(1)`). Always synchronous.

```go
// Critical error (sync)
logfile.Critical(nil, false, err, "Database connection lost")

// Fatal error (always sync, will exit program)
logfile.Fatal(nil, err, "Failed to bind to required port 8080")
```

---

## **Operation Tracing (MessageLog)**

For tracing a single request or operation through multiple steps, use the `MessageLog` context.

1. **Start** the operation to get a `MessageLog` (`ml`).
2. **Pass** `ml` to all subsequent logging calls (`OperationStep`, `Info`, `Error`, etc.).
3. **Complete** or **Error** the operation to log a final summary.

```go
func HandleUserRequest(w http.ResponseWriter, r *http.Request) {
 // 1. Start the operation
 ml := logfile.OperationStart(
  "HandleUserRequest", // Action name
  "ref-trx-12345",     // Reference/Transaction ID
  "User",              // Entity
  "API",               // Transaction Type
  r.URL.Path,          // URL
 )

 // 2. Log steps
 logfile.OperationStep(ml, true, "Step1_ValidateInput", "Input validation complete")

 // ... do some work ...
 user, err := db.GetUser(123)
 if err != nil {
  // 3a. Log an error
  logfile.OperationError(ml, true, err, "Failed to get user from DB")
  return
 }

 logfile.OperationStep(ml, true, "Step2_GetUser", "Database query successful")

 // ... do more work ...

 // 3b. Complete the operation
 logfile.OperationComplete(ml, true, "User request handled successfully",
  slog.Int("user_id", user.ID),
 )
}
```

If not you can also just create a new `MessageLog` and use it

```go
logfile.CreateLogger()
defer logfile.Shutdown()

// 1. Create message log
m := logfile.CreateMessageLog("TEST", "", "CLIENT", "TOPIC", "/test")

logfile.Info(m, false, "testing message")

// 2. Update step
m = m.WithStep(2)
logfile.Warn(m, false, "testing message 2")
```

---

## **Performance Tracking**

### **DurationTracker**

A high-performance stopwatch for timing parts of an operation.

```go
tracker := logfile.NewDurationTracker("MyComplexFunction")

// ... code for part 1 ...
step1Duration := tracker.NextStep()
logfile.Info(nil, true, "Part 1 finished",
 slog.String("duration", step1Duration.String()))

// ... code for part 2 ...
step2Duration := tracker.NextStep()
logfile.Info(nil, true, "Part 2 finished",
 slog.String("duration", step2Duration.String()))

// Log the final completion
tracker.LogComplete(nil, true, "MyComplexFunction finished")
```

---

### **PerformanceBenchmark**

Collects many duration samples to calculate min, max, and avg statistics.
Useful for monitoring database queries or API calls.

```go
// Register a global benchmark on startup
dbBenchmark := logfile.RegisterBenchmark("db_query_GetUser", 100) // Keep 100 samples

// In your function:
start := time.Now()
user, err := db.GetUser(123)
duration := time.Since(start)

// Record the duration
dbBenchmark.Record(duration)

// You can also log the stats periodically:
dbBenchmark.LogStats(nil, true)
```

---

### **TimedOperation**

A simple wrapper to time a function call.

```go
duration, err := logfile.TimedOperation("MyFunction", func() error {
 // ... your code ...
 return nil
})

// Or use the version that logs automatically:
err := logfile.TimedOperationWithLogging(nil, true, "MyFunction", func() error {
 // ... your code ...
 return nil
})
```

## Performance

| Benchmark Name                                      | Iterations | ns/op | B/op   | allocs/op | Extra Info                                  |
| --------------------------------------------------- | ---------- | ----- | ------ | --------- | ------------------------------------------- |
| BenchmarkSimpleAttributes_Async-14                  | 18,686,899 | 669.8 | 696    | 13        |                                             |
| BenchmarkSimpleAttributes_Sync-14                   | 33,890,847 | 458.6 | 480    | 7         |                                             |
| BenchmarkSimpleAttributes_Parallel-14               | 44,223,838 | 340.9 | 568    | 11        |                                             |
| BenchmarkSmallStruct_Async-14                       | 24,857,358 | 496.7 | 480    | 8         |                                             |
| BenchmarkSmallStruct_Sync-14                        | 36,669,570 | 355.6 | 416    | 6         |                                             |
| BenchmarkSmallStruct_vs_Decomposed/WithAny-14       | 25,824,816 | 461.5 | 480    | 8         |                                             |
| BenchmarkSmallStruct_vs_Decomposed/Decomposed-14    | 15,753,409 | 688.6 | 856    | 16        |                                             |
| BenchmarkMediumComplexity-14                        | 29,663,442 | 427.3 | 512    | 8         |                                             |
| BenchmarkLargeComplexObject-14                      | 29,924,834 | 419.0 | 512    | 8         |                                             |
| BenchmarkLargeComplexObject_Summary-14              | 19,736,838 | 638.7 | 808    | 14        |                                             |
| BenchmarkOperationTracking-14                       | 1,293,019  | 9,977 | 13,914 | 115       |                                             |
| BenchmarkHighTraffic_Concurrent-14                  | 51,676,456 | 289.4 | 552    | 10        |                                             |
| BenchmarkComparison_AsyncVsSync/Async-14            | 19,174,500 | 524.2 | 424    | 8         |                                             |
| BenchmarkComparison_AsyncVsSync/Sync-14             | 32,516,635 | 372.8 | 352    | 5         |                                             |
| BenchmarkComparison_AttributeTypes/String-14        | 28,499,211 | 459.2 | 432    | 8         |                                             |
| BenchmarkComparison_AttributeTypes/Int-14           | 27,071,209 | 467.3 | 424    | 8         |                                             |
| BenchmarkComparison_AttributeTypes/Bool-14          | 29,140,542 | 384.0 | 416    | 7         |                                             |
| BenchmarkComparison_AttributeTypes/Time-14          | 29,343,985 | 479.9 | 440    | 8         |                                             |
| BenchmarkComparison_AttributeTypes/SmallStruct-14   | 26,224,881 | 448.7 | 480    | 8         |                                             |
| BenchmarkComparison_AttributeTypes/Map10Keys-14     | 13,294,248 | 1071  | 1080   | 11        |                                             |
| BenchmarkComparison_AttributeTypes/Slice100Items-14 | 14,994,357 | 773.1 | 1360   | 10        |                                             |
| BenchmarkMemoryAllocation-14                        | 19,291,318 | 572.6 | 696    | 13        |                                             |
| BenchmarkLatency_Percentiles-14                     | 25,752,589 | 405.8 | 431    | 7         | p50=294ns, p95=307ns, p99=297ns, p999=310ns |
