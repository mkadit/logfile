// message_log.go - Thread-safe version
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
	ContextMessageKey = "LogfileContextMsgKey"
)

var ErrMissingMessage = errors.New("error missing log message")

// MessageLog contains metadata for log messages, with thread-safe operations
type MessageLog struct {
	InternalID string `json:"internal_id"`
	Action     string `json:"action"`
	Flow       string `json:"flow"`
	Step       int    `json:"step"`
	Entity     string `json:"entity"`
	SystemName string `json:"system_name"`
	ReffTrx    string `json:"reff_trx"`
	RC         string `json:"rc"`
	TypeTrx    string `json:"type_trx"`
	Header     string `json:"header"`
	URL        string `json:"url"`
	Msg        string `json:"msg"`

	// Duration tracking fields
	StartTime time.Time `json:"start_time"`
	LastTime  time.Time `json:"last_time"`

	// Thread-safe step durations tracking
	StepDurations map[int]time.Duration `json:"step_durations,omitempty"`
	mu            sync.RWMutex          `json:"-"` // Protects all mutable fields
}

// CreateMessageLog creates a new message log entry with start time tracking
func CreateMessageLog(action string, reffTrx string, entity string, typeTrx string, url string) *MessageLog {
	httpString := "request from"
	ctxReqID := uuid.New().String()
	msg := fmt.Sprintf("%s %s (%s):", httpString, entity, url)
	now := time.Now()

	return &MessageLog{
		SystemName:    SystemName,
		InternalID:    ctxReqID,
		Action:        action,
		Flow:          "IN",
		Step:          1,
		Entity:        entity,
		ReffTrx:       reffTrx,
		TypeTrx:       typeTrx,
		URL:           url,
		Msg:           msg,
		StartTime:     now,
		LastTime:      now,
		StepDurations: make(map[int]time.Duration),
	}
}

// UpdateMessageLog updates message transaction state and updates the last time
func (ml *MessageLog) UpdateMessageLog(flow string, entity string, typeTrx string) {
	if ml == nil {
		return
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	var httpString string
	ml.Entity = entity
	ml.TypeTrx = typeTrx

	// Record step duration before incrementing step
	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration)
	}
	ml.StepDurations[ml.Step] = ml.getDurationSinceLastLogUnsafe()

	ml.Step = ml.Step + 1
	ml.LastTime = time.Now()

	if flow == "IN" {
		httpString = "incoming data"
	} else {
		httpString = "outgoing data"
	}

	msg := fmt.Sprintf("%s %s (%s):", httpString, entity, ml.URL)
	ml.Flow = flow
	ml.Msg = msg
}

// GetDurationSinceStart returns the duration since the MessageLog was created
func (ml *MessageLog) GetDurationSinceStart() time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if ml.StartTime.IsZero() {
		return 0
	}
	return time.Since(ml.StartTime)
}

// GetDurationSinceLastLog returns the duration since the last log operation
func (ml *MessageLog) GetDurationSinceLastLog() time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	return ml.getDurationSinceLastLogUnsafe()
}

// getDurationSinceLastLogUnsafe is the internal version without locking
func (ml *MessageLog) getDurationSinceLastLogUnsafe() time.Duration {
	if ml.LastTime.IsZero() {
		return 0
	}
	return time.Since(ml.LastTime)
}

// GetStepDuration returns the duration for a specific step
func (ml *MessageLog) GetStepDuration(step int) time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if ml.StepDurations == nil {
		return 0
	}
	return ml.StepDurations[step]
}

// GetCurrentStepDuration returns the duration of the current step
func (ml *MessageLog) GetCurrentStepDuration() time.Duration {
	return ml.GetDurationSinceLastLog()
}

// UpdateLastTime updates the last time to current time
func (ml *MessageLog) UpdateLastTime() {
	if ml == nil {
		return
	}
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.LastTime = time.Now()
}

// RecordStepDuration records the duration for the current step and advances to next step
func (ml *MessageLog) RecordStepDuration() {
	if ml == nil {
		return
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration)
	}

	// Record current step duration
	ml.StepDurations[ml.Step] = ml.getDurationSinceLastLogUnsafe()

	// Advance to next step
	ml.Step = ml.Step + 1
	ml.LastTime = time.Now()
}

// SafeRecordStepDuration safely records step duration and returns the recorded duration
func (ml *MessageLog) SafeRecordStepDuration() time.Duration {
	if ml == nil {
		return 0
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration)
	}

	duration := ml.getDurationSinceLastLogUnsafe()
	ml.StepDurations[ml.Step] = duration
	ml.LastTime = time.Now()

	return duration
}

// Clone creates a thread-safe copy of the MessageLog
func (ml *MessageLog) Clone() *MessageLog {
	if ml == nil {
		return nil
	}

	ml.mu.RLock()
	defer ml.mu.RUnlock()

	// Deep copy step durations
	stepDurations := make(map[int]time.Duration)
	if ml.StepDurations != nil {
		for k, v := range ml.StepDurations {
			stepDurations[k] = v
		}
	}

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
		// Note: mu is not copied - each clone gets a new mutex
	}
}

// WithStep creates a new MessageLog with updated step
func (ml *MessageLog) WithStep(step int) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Step = step
	}
	return clone
}

// WithFlow creates a new MessageLog with updated flow
func (ml *MessageLog) WithFlow(flow string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Flow = flow
	}
	return clone
}

// WithEntity creates a new MessageLog with updated entity
func (ml *MessageLog) WithEntity(entity string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Entity = entity
	}
	return clone
}

// WithRC creates a new MessageLog with updated response code
func (ml *MessageLog) WithRC(rc string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.RC = rc
	}
	return clone
}

// WithFeature creates a new MessageLog with updated actions and url
func (ml *MessageLog) WithFeature(action string, url string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Action = action
		clone.URL = url
	}
	return clone
}

// ToSlogAttrs converts the MessageLog fields into a slice of slog.Attr for structured logging.
func (m *MessageLog) ToSlogAttrs() []slog.Attr {
	if m == nil {
		return []slog.Attr{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	attrs := []slog.Attr{}
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

	// Add duration information
	if !m.StartTime.IsZero() {
		attrs = append(attrs, slog.String("duration_total", time.Since(m.StartTime).String()))
	}

	// Convert to string map to avoid race conditions during JSON serialization
	if m.StepDurations != nil {
		stepDurationsCopy := make(map[string]string, len(m.StepDurations))
		for step, duration := range m.StepDurations {
			stepDurationsCopy[fmt.Sprintf("step_%d", step)] = duration.String()
		}
		attrs = append(attrs, slog.Any("step_durations", stepDurationsCopy))
	}

	return attrs
}

// ToSlogAttrsWithCustomDuration allows adding custom duration measurements
func (m *MessageLog) ToSlogAttrsWithCustomDuration(customDurations map[string]time.Duration) []slog.Attr {
	attrs := m.ToSlogAttrs()

	// Add custom durations
	for key, duration := range customDurations {
		attrs = append(attrs, slog.String(fmt.Sprintf("duration_%s", key), duration.String()))
	}

	return attrs
}

// GetDurationSummary returns a thread-safe summary of all step durations
func (ml *MessageLog) GetDurationSummary() map[string]interface{} {
	if ml == nil {
		return nil
	}

	ml.mu.RLock()
	defer ml.mu.RUnlock()

	summary := make(map[string]interface{})
	summary["total_duration"] = time.Since(ml.StartTime).String()
	summary["current_step_duration"] = ml.getDurationSinceLastLogUnsafe().String()
	summary["current_step"] = ml.Step

	if ml.StepDurations != nil && len(ml.StepDurations) > 0 {
		stepDurations := make(map[string]string)
		for step, duration := range ml.StepDurations {
			stepDurations[fmt.Sprintf("step_%d", step)] = duration.String()
		}
		summary["step_durations"] = stepDurations
	}

	return summary
}
