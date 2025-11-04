// message_log.go - Defines the MessageLog struct for operation tracking.
package logfile

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// ContextMessageKey is a key used to store and retrieve a MessageLog
	// from a context.Context.
	ContextMessageKey = "LogfileContextMsgKey"

	// defaultStepCapacity is a pre-allocation hint for the StepDurations map.
	// It avoids multiple map re-allocations for typical operations.
	defaultStepCapacity = 50 // Pre-allocate for 50 steps
)

// ErrMissingMessage indicates that a log message was expected but not provided.
var ErrMissingMessage = errors.New("error missing log message")

// MessageLog tracks the state and timing of a multi-step operation or transaction.
// It is designed to be passed through an operation's lifecycle to provide
// consistent, contextual logging.
type MessageLog struct {
	InternalID string `json:"internal_id"` // Unique ID for this log entry/operation.
	Action     string `json:"action"`      // Name of the operation (e.g., "ProcessPayment").
	Flow       string `json:"flow"`        // Direction of data (e.g., "IN" or "OUT").
	Step       int    `json:"step"`        // The current step number in the operation.
	Entity     string `json:"entity"`      // The service or component performing the action.
	SystemName string `json:"system_name"` // The name of the overall application.
	ReffTrx    string `json:"reff_trx"`    // External transaction reference ID.
	RC         string `json:"rc"`          // Response code.
	TypeTrx    string `json:"type_trx"`    // Type of transaction (e.g., "CREDIT").
	Header     string `json:"header"`      // Associated header data (e.g., HTTP headers).
	URL        string `json:"url"`         // URL associated with the operation.
	Msg        string `json:"msg"`         // A base message for the operation.

	StartTime time.Time `json:"start_time"` // When the operation started.
	LastTime  time.Time `json:"last_time"`  // When the last step was recorded.

	// StepDurations stores the duration of each completed step.
	// The key is the step number.
	StepDurations map[int]time.Duration `json:"step_durations,omitempty"`
	// mu protects fields that are modified during the operation (Step, LastTime, StepDurations).
	mu sync.RWMutex `json:"-"`
}

// CreateMessageLog creates and initializes a new MessageLog for tracking an operation.
func CreateMessageLog(action string, reffTrx string, entity string, typeTrx string, url string) *MessageLog {
	httpString := "request from"
	// Generate a unique internal ID for this operation.
	ctxReqID := uuid.New().String()
	msg := fmt.Sprintf("%s %s (%s):", httpString, entity, url)
	now := time.Now()

	return &MessageLog{
		SystemName:    SystemName,
		InternalID:    ctxReqID,
		Action:        action,
		Flow:          "IN", // Default flow is "IN"
		Step:          1,    // Start at step 1
		Entity:        entity,
		ReffTrx:       reffTrx,
		TypeTrx:       typeTrx,
		URL:           url,
		Msg:           msg,
		StartTime:     now,
		LastTime:      now,
		StepDurations: make(map[int]time.Duration, defaultStepCapacity), // Pre-allocate map
	}
}

// UpdateMessageLog updates the state of the MessageLog for a new phase or flow.
// It records the duration of the *previous* step before updating.
func (ml *MessageLog) UpdateMessageLog(flow string, entity string, typeTrx string) {
	if ml == nil {
		return
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	var httpString string
	ml.Entity = entity
	ml.TypeTrx = typeTrx

	// Ensure the map is initialized (should be by CreateMessageLog, but good practice).
	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration, defaultStepCapacity)
	}
	// Record the duration of the step that is *ending*.
	ml.StepDurations[ml.Step] = ml.getDurationSinceLastLogUnsafe()

	// Advance to the next step and reset the timer.
	ml.Step++
	ml.LastTime = time.Now()

	if flow == "IN" {
		httpString = "incoming data"
	} else {
		httpString = "outgoing data"
	}

	// Update the base message.
	msg := fmt.Sprintf("%s %s (%s):", httpString, entity, ml.URL)
	ml.Flow = flow
	ml.Msg = msg
}

// GetDurationSinceStart returns the total duration since the MessageLog was created.
func (ml *MessageLog) GetDurationSinceStart() time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock() // Use read lock for safety.
	defer ml.mu.RUnlock()

	if ml.StartTime.IsZero() {
		return 0
	}
	return time.Since(ml.StartTime)
}

// GetDurationSinceLastLog returns the duration since the last step was recorded.
// This represents the duration of the *current, active* step.
func (ml *MessageLog) GetDurationSinceLastLog() time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	return ml.getDurationSinceLastLogUnsafe()
}

// getDurationSinceLastLogUnsafe is the internal, non-locking version of GetDurationSinceLastLog.
// It should only be called by methods that already hold the mutex.
func (ml *MessageLog) getDurationSinceLastLogUnsafe() time.Duration {
	if ml.LastTime.IsZero() {
		return 0
	}
	return time.Since(ml.LastTime)
}

// GetStepDuration returns the recorded duration for a specific *completed* step.
func (ml *MessageLog) GetStepDuration(step int) time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if ml.StepDurations == nil {
		return 0
	}
	// Reading from a map is thread-safe for concurrent reads, but
	// we use RLock for consistency with other methods.
	return ml.StepDurations[step]
}

// GetCurrentStepDuration is an alias for GetDurationSinceLastLog.
func (ml *MessageLog) GetCurrentStepDuration() time.Duration {
	return ml.GetDurationSinceLastLog()
}

// UpdateLastTime updates the LastTime to time.Now().
// This is used to "reset" the step timer without advancing the step counter.
func (ml *MessageLog) UpdateLastTime() {
	if ml == nil {
		return
	}
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.LastTime = time.Now()
}

// RecordStepDuration records the duration of the current step and advances to the next.
func (ml *MessageLog) RecordStepDuration() {
	if ml == nil {
		return
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration, defaultStepCapacity)
	}

	// Record duration of the step that just finished.
	ml.StepDurations[ml.Step] = ml.getDurationSinceLastLogUnsafe()
	// Advance to the next step.
	ml.Step++
	// Reset the timer for the new step.
	ml.LastTime = time.Now()
}

// SafeRecordStepDuration records the duration, advances the step, and returns the duration
// that was just recorded.
func (ml *MessageLog) SafeRecordStepDuration() time.Duration {
	if ml == nil {
		return 0
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration, defaultStepCapacity)
	}

	// Get duration before resetting LastTime.
	duration := ml.getDurationSinceLastLogUnsafe()
	ml.StepDurations[ml.Step] = ml.StepDurations[ml.Step] + duration
	// Note: This function does not increment ml.Step.
	// This is a subtle difference from RecordStepDuration.
	// Based on its use in `logToSpecificLogger`, this function is
	// *only* for recording the *current* step's duration *at the time of logging*.
	ml.LastTime = time.Now()

	return duration
}

// Clone creates a thread-safe deep copy of the MessageLog.
// This is useful for passing a snapshot of the log to another goroutine.
func (ml *MessageLog) Clone() *MessageLog {
	if ml == nil {
		return nil
	}

	ml.mu.RLock() // Use read lock while copying.
	defer ml.mu.RUnlock()

	// Determine capacity for the new map.
	capacity := len(ml.StepDurations)
	if capacity < defaultStepCapacity {
		capacity = defaultStepCapacity
	}

	// Create a new map and deep copy the durations.
	stepDurations := make(map[int]time.Duration, capacity)
	if ml.StepDurations != nil {
		for k, v := range ml.StepDurations {
			stepDurations[k] = v
		}
	}

	// Return a new MessageLog struct with copied values.
	return &MessageLog{
		InternalID:    ml.InternalID,
		Action:        ml.Action,
		Flow:          ml.Flow,
		Step:          ml.Step,
		Entity:        ml.Entity,
		SystemName:    ml.SystemName,
		ReffTrx:       ml.ReffTrx,
		RC:            ml.RC,
		TypeTrx:       ml.TypeTrx,
		Header:        ml.Header,
		URL:           ml.URL,
		Msg:           ml.Msg,
		StartTime:     ml.StartTime,
		LastTime:      ml.LastTime,
		StepDurations: stepDurations,
		// Note: The new copy has its own mutex, initialized to zero value.
	}
}

// WithStep returns a *clone* of the MessageLog with the Step field updated.
func (ml *MessageLog) WithStep(step int) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Step = step
	}
	return clone
}

// WithFlow returns a *clone* of the MessageLog with the Flow field updated.
func (ml *MessageLog) WithFlow(flow string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Flow = flow
	}
	return clone
}

// WithEntity returns a *clone* of the MessageLog with the Entity field updated.
func (ml *MessageLog) WithEntity(entity string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Entity = entity
	}
	return clone
}

// WithRC returns a *clone* of the MessageLog with the RC (Response Code) field updated.
func (ml *MessageLog) WithRC(rc string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.RC = rc
	}
	return clone
}

// WithFeature returns a *clone* of the MessageLog with the Action and URL fields updated.
func (ml *MessageLog) WithFeature(action string, url string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Action = action
		clone.URL = url
	}
	return clone
}

// ToSlogAttrs converts the MessageLog's fields into a slice of slog.Attr
// for structured logging. This is a thread-safe operation.
func (m *MessageLog) ToSlogAttrs() []slog.Attr {
	if m == nil {
		return []slog.Attr{}
	}

	m.mu.RLock() // Use read lock.
	defer m.mu.RUnlock()

	// Pre-allocate slice with a reasonable capacity.
	attrs := make([]slog.Attr, 0, 15)

	// Add all non-empty fields as attributes.
	if m.InternalID != "" {
		attrs = append(attrs, slog.String("internal_id", m.InternalID))
	}
	if m.Action != "" {
		attrs = append(attrs, slog.String("action", m.Action))
	}
	if m.Flow != "" {
		attrs = append(attrs, slog.String("flow", m.Flow))
	}
	if m.Step != 0 {
		attrs = append(attrs, slog.Int("step", m.Step))
	}
	if m.Entity != "" {
		attrs = append(attrs, slog.String("entity", m.Entity))
	}
	if m.SystemName != "" {
		attrs = append(attrs, slog.String("system_name", m.SystemName))
	}
	if m.ReffTrx != "" {
		attrs = append(attrs, slog.String("reff_trx", m.ReffTrx))
	}
	if m.RC != "" {
		attrs = append(attrs, slog.String("rc", m.RC))
	}
	if m.TypeTrx != "" {
		attrs = append(attrs, slog.String("type_trx", m.TypeTrx))
	}
	if m.Header != "" {
		attrs = append(attrs, slog.String("header", m.Header))
	}
	if m.URL != "" {
		attrs = append(attrs, slog.String("url", m.URL))
	}

	// Add total duration.
	if !m.StartTime.IsZero() {
		attrs = append(attrs, slog.String("duration_total", time.Since(m.StartTime).String()))
	}

	// Add step durations as a map[string]string for clean logging.
	if m.StepDurations != nil && len(m.StepDurations) > 0 {
		stepDurationsCopy := make(map[string]string, len(m.StepDurations))
		for step, duration := range m.StepDurations {
			stepDurationsCopy[fmt.Sprintf("step_%d", step)] = duration.String()
		}
		attrs = append(attrs, slog.Any("step_durations", stepDurationsCopy))
	}

	return attrs
}

// ToSlogAttrsWithCustomDuration is a convenience function to append
// custom duration metrics to the standard MessageLog attributes.
func (m *MessageLog) ToSlogAttrsWithCustomDuration(customDurations map[string]time.Duration) []slog.Attr {
	// Get the base attributes.
	attrs := m.ToSlogAttrs()

	// Add custom durations.
	for key, duration := range customDurations {
		attrs = append(attrs, slog.String(fmt.Sprintf("duration_%s", key), duration.String()))
	}

	return attrs
}

// GetDurationSummary returns a thread-safe map summarizing all step durations.
// The map is suitable for logging as a structured attribute.
func (ml *MessageLog) GetDurationSummary() map[string]interface{} {
	if ml == nil {
		return nil
	}

	ml.mu.RLock()
	defer ml.mu.RUnlock()

	// Create the summary map.
	summary := make(map[string]interface{}, 4)
	summary["total_duration"] = time.Since(ml.StartTime).String()
	summary["current_step_duration"] = ml.getDurationSinceLastLogUnsafe().String()
	summary["current_step"] = ml.Step

	// Create a copy of step durations, formatted as strings.
	if ml.StepDurations != nil && len(ml.StepDurations) > 0 {
		stepDurations := make(map[string]string, len(ml.StepDurations))
		for step, duration := range ml.StepDurations {
			stepDurations[fmt.Sprintf("step_%d", step)] = duration.String()
		}
		summary["step_durations"] = stepDurations
	}

	return summary
}
