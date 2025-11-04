// message_log.go - Defines the MessageLog struct for operation tracking.
package logfile

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	ContextMessageKey   = "LogfileContextMsgKey"
	defaultStepCapacity = 50
)

var ErrMissingMessage = errors.New("error missing log message")

// MessageLog tracks the state and timing of a multi-step operation or transaction.
// THREAD-SAFETY: All methods are thread-safe. Use Clone() when passing to async operations.
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

	StartTime time.Time `json:"start_time"`
	LastTime  time.Time `json:"last_time"`

	StepDurations map[int]time.Duration `json:"step_durations,omitempty"`
	mu            sync.RWMutex          `json:"-"`
}

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
		StepDurations: make(map[int]time.Duration, defaultStepCapacity),
	}
}

// UpdateMessageLog updates the state of the MessageLog for a new phase or flow.
// CRITICAL: This modifies the MessageLog. Do not call during async logging.
func (ml *MessageLog) UpdateMessageLog(flow string, entity string, typeTrx string) {
	if ml == nil {
		return
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	var httpString string
	ml.Entity = entity
	ml.TypeTrx = typeTrx

	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration, defaultStepCapacity)
	}
	ml.StepDurations[ml.Step] = ml.getDurationSinceLastLogUnsafe()

	ml.Step++
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

func (ml *MessageLog) GetDurationSinceLastLog() time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	return ml.getDurationSinceLastLogUnsafe()
}

func (ml *MessageLog) getDurationSinceLastLogUnsafe() time.Duration {
	if ml.LastTime.IsZero() {
		return 0
	}
	return time.Since(ml.LastTime)
}

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

func (ml *MessageLog) GetCurrentStepDuration() time.Duration {
	return ml.GetDurationSinceLastLog()
}

// GetCurrentStepDurationSnapshot returns the duration WITHOUT modifying MessageLog.
// USE THIS in logging functions to avoid race conditions.
func (ml *MessageLog) GetCurrentStepDurationSnapshot() time.Duration {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	return ml.getDurationSinceLastLogUnsafe()
}

func (ml *MessageLog) UpdateLastTime() {
	if ml == nil {
		return
	}
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.LastTime = time.Now()
}

// RecordStepDuration records the duration of the current step and advances to the next.
// CRITICAL: This modifies the MessageLog. Do not call during async logging.
func (ml *MessageLog) RecordStepDuration() {
	if ml == nil {
		return
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.StepDurations == nil {
		ml.StepDurations = make(map[int]time.Duration, defaultStepCapacity)
	}

	ml.StepDurations[ml.Step] = ml.getDurationSinceLastLogUnsafe()
	ml.Step++
	ml.LastTime = time.Now()
}

// DEPRECATED: Use GetCurrentStepDurationSnapshot() instead for logging.
// This method was previously unsafe and modified LastTime during logging.
func (ml *MessageLog) SafeRecordStepDuration() time.Duration {
	return ml.GetCurrentStepDurationSnapshot()
}

// GetCurrentStep returns the current step number (thread-safe read).
func (ml *MessageLog) GetCurrentStep() int {
	if ml == nil {
		return 0
	}
	ml.mu.RLock()
	defer ml.mu.RUnlock()
	return ml.Step
}

func (ml *MessageLog) Clone() *MessageLog {
	if ml == nil {
		return nil
	}

	ml.mu.RLock()
	defer ml.mu.RUnlock()

	capacity := max(len(ml.StepDurations), defaultStepCapacity)

	stepDurations := make(map[int]time.Duration, capacity)
	if ml.StepDurations != nil {
		maps.Copy(stepDurations, ml.StepDurations)
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
	}
}

func (ml *MessageLog) WithStep(step int) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Step = step
	}
	return clone
}

func (ml *MessageLog) WithFlow(flow string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Flow = flow
	}
	return clone
}

func (ml *MessageLog) WithEntity(entity string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Entity = entity
	}
	return clone
}

func (ml *MessageLog) WithRC(rc string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.RC = rc
	}
	return clone
}

func (ml *MessageLog) WithFeature(action string, url string) *MessageLog {
	clone := ml.Clone()
	if clone != nil {
		clone.Action = action
		clone.URL = url
	}
	return clone
}

func (m *MessageLog) ToSlogAttrs() []slog.Attr {
	if m == nil {
		return []slog.Attr{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	attrs := make([]slog.Attr, 0, 15)

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

	if !m.StartTime.IsZero() {
		attrs = append(attrs, slog.String("duration_total", time.Since(m.StartTime).String()))
	}

	if len(m.StepDurations) > 0 {
		stepDurationsCopy := make(map[string]string, len(m.StepDurations))
		for step, duration := range m.StepDurations {
			stepDurationsCopy[fmt.Sprintf("step_%d", step)] = duration.String()
		}
		attrs = append(attrs, slog.Any("step_durations", stepDurationsCopy))
	}

	return attrs
}

func (m *MessageLog) ToSlogAttrsWithCustomDuration(customDurations map[string]time.Duration) []slog.Attr {
	attrs := m.ToSlogAttrs()

	for key, duration := range customDurations {
		attrs = append(attrs, slog.String(fmt.Sprintf("duration_%s", key), duration.String()))
	}

	return attrs
}

func (ml *MessageLog) GetDurationSummary() map[string]any {
	if ml == nil {
		return nil
	}

	ml.mu.RLock()
	defer ml.mu.RUnlock()

	summary := make(map[string]any, 4)
	summary["total_duration"] = time.Since(ml.StartTime).String()
	summary["current_step_duration"] = ml.getDurationSinceLastLogUnsafe().String()
	summary["current_step"] = ml.Step

	if len(ml.StepDurations) > 0 {
		stepDurations := make(map[string]string, len(ml.StepDurations))
		for step, duration := range ml.StepDurations {
			stepDurations[fmt.Sprintf("step_%d", step)] = duration.String()
		}
		summary["step_durations"] = stepDurations
	}

	return summary
}
