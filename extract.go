// logfile/extract.go - Data extraction utilities
package logfile

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// LogData represents the complete structure of captured JSON logs
// This struct handles all fields that can appear in logs, including MessageLog fields
type LogData struct {
	// Base log fields (always present)
	EventType string `json:"event_type"`
	Timestamp string `json:"timestamp"`
	Msg       string `json:"msg"`

	// Optional base fields
	Level *string `json:"level,omitempty"`
	Error *string `json:"error,omitempty"`

	// MessageLog fields (present when logging with MessageLog)
	InternalID *string `json:"internal_id,omitempty"`
	Action     *string `json:"action,omitempty"`
	Flow       *string `json:"flow,omitempty"`
	Step       *int    `json:"step,omitempty"`
	Entity     *string `json:"entity,omitempty"`
	SystemName *string `json:"system_name,omitempty"`
	ReffTrx    *string `json:"reff_trx,omitempty"`
	RC         *string `json:"rc,omitempty"`
	TypeTrx    *string `json:"type_trx,omitempty"`
	Header     *string `json:"header,omitempty"`
	URL        *string `json:"url,omitempty"`

	// Duration and performance fields
	DurationTotal            *string      `json:"duration_total,omitempty"`
	DurationStepActive       *string      `json:"duration_step_active,omitempty"`
	DurationStepCompleted    *string      `json:"duration_step_completed,omitempty"`
	StepDurations            *interface{} `json:"step_durations,omitempty"`
	PerformanceFlagSlow      *bool        `json:"performance_flag_slow,omitempty"`
	PerformanceFlagManySteps *bool        `json:"performance_flag_many_steps,omitempty"`

	// All other fields from slog attributes (including any slog.Group data)
	// These are captured dynamically during JSON parsing
	OtherFields map[string]interface{} `json:"-"`
}

// ParseLogJSON parses captured JSON string into LogData struct
func ParseLogJSON(jsonStr string) (*LogData, error) {
	// First parse into raw map to handle all fields dynamically
	var rawData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawData); err != nil {
		return nil, fmt.Errorf("failed to parse log JSON: %w", err)
	}

	logData := &LogData{
		OtherFields: make(map[string]interface{}),
	}

	// Extract known fields from raw data
	if eventType, ok := rawData["event_type"].(string); ok {
		logData.EventType = eventType
	}
	if timestamp, ok := rawData["timestamp"].(string); ok {
		logData.Timestamp = timestamp
	}
	if msg, ok := rawData["msg"].(string); ok {
		logData.Msg = msg
	}

	// Optional base fields
	if level, ok := rawData["level"].(string); ok {
		logData.Level = &level
	}
	if err, ok := rawData["error"].(string); ok {
		logData.Error = &err
	}

	// MessageLog fields
	if internalID, ok := rawData["internal_id"].(string); ok {
		logData.InternalID = &internalID
	}
	if action, ok := rawData["action"].(string); ok {
		logData.Action = &action
	}
	if flow, ok := rawData["flow"].(string); ok {
		logData.Flow = &flow
	}
	if step, ok := rawData["step"].(float64); ok { // JSON numbers are float64
		stepInt := int(step)
		logData.Step = &stepInt
	}
	if entity, ok := rawData["entity"].(string); ok {
		logData.Entity = &entity
	}
	if systemName, ok := rawData["system_name"].(string); ok {
		logData.SystemName = &systemName
	}
	if reffTrx, ok := rawData["reff_trx"].(string); ok {
		logData.ReffTrx = &reffTrx
	}
	if rc, ok := rawData["rc"].(string); ok {
		logData.RC = &rc
	}
	if typeTrx, ok := rawData["type_trx"].(string); ok {
		logData.TypeTrx = &typeTrx
	}
	if header, ok := rawData["header"].(string); ok {
		logData.Header = &header
	}
	if url, ok := rawData["url"].(string); ok {
		logData.URL = &url
	}

	// Duration and performance fields
	if durationTotal, ok := rawData["duration_total"].(string); ok {
		logData.DurationTotal = &durationTotal
	}
	if durationStepActive, ok := rawData["duration_step_active"].(string); ok {
		logData.DurationStepActive = &durationStepActive
	}
	if durationStepCompleted, ok := rawData["duration_step_completed"].(string); ok {
		logData.DurationStepCompleted = &durationStepCompleted
	}
	if stepDurations, ok := rawData["step_durations"]; ok {
		logData.StepDurations = &stepDurations
	}
	if perfSlow, ok := rawData["performance_flag_slow"].(bool); ok {
		logData.PerformanceFlagSlow = &perfSlow
	}
	if perfManySteps, ok := rawData["performance_flag_many_steps"].(bool); ok {
		logData.PerformanceFlagManySteps = &perfManySteps
	}

	// Store all other fields dynamically
	knownFields := map[string]bool{
		"event_type": true, "timestamp": true, "msg": true, "level": true, "error": true,
		"internal_id": true, "action": true, "flow": true, "step": true, "entity": true,
		"system_name": true, "reff_trx": true, "rc": true, "type_trx": true, "header": true, "url": true,
		"duration_total": true, "duration_step_active": true, "duration_step_completed": true,
		"step_durations": true, "performance_flag_slow": true, "performance_flag_many_steps": true,
	}

	for key, value := range rawData {
		if !knownFields[key] {
			logData.OtherFields[key] = value
		}
	}

	return logData, nil
}

// HasMessageLog checks if the log contains MessageLog data
func (ld *LogData) HasMessageLog() bool {
	return ld.InternalID != nil || ld.Action != nil || ld.Flow != nil ||
		ld.Entity != nil || ld.ReffTrx != nil || ld.TypeTrx != nil
}

// GetMessageLogField returns a MessageLog field as string pointer
func (ld *LogData) GetMessageLogField(field string) (*string, bool) {
	var value *string
	switch field {
	case "internal_id":
		value = ld.InternalID
	case "action":
		value = ld.Action
	case "flow":
		value = ld.Flow
	case "entity":
		value = ld.Entity
	case "system_name":
		value = ld.SystemName
	case "reff_trx":
		value = ld.ReffTrx
	case "rc":
		value = ld.RC
	case "type_trx":
		value = ld.TypeTrx
	case "header":
		value = ld.Header
	case "url":
		value = ld.URL
	default:
		return nil, false
	}

	if value != nil {
		return value, true
	}
	return nil, false
}

// GetStep returns the step number as int
func (ld *LogData) GetStep() (int, bool) {
	if ld.Step != nil {
		return *ld.Step, true
	}
	return 0, false
}

// GetPerformanceFlag returns performance flag values
func (ld *LogData) GetPerformanceFlag(flag string) (bool, bool) {
	var value *bool
	switch flag {
	case "slow":
		value = ld.PerformanceFlagSlow
	case "many_steps":
		value = ld.PerformanceFlagManySteps
	default:
		return false, false
	}

	if value != nil {
		return *value, true
	}
	return false, false
}

// ExtractData extracts all other fields (including slog.Group data) as map[string]interface{}
func ExtractData(logData *LogData) (map[string]interface{}, bool) {
	if logData == nil || len(logData.OtherFields) == 0 {
		return nil, false
	}

	// Return a copy to prevent modification of internal state
	result := make(map[string]interface{}, len(logData.OtherFields))
	for k, v := range logData.OtherFields {
		result[k] = v
	}
	return result, true
}

// ExtractGroup extracts a slog.Group by name from the other fields
// slog.Group creates nested structures, so we navigate to find requested group
func ExtractGroup(data map[string]interface{}, groupName string) (map[string]interface{}, bool) {
	if data == nil {
		return nil, false
	}

	// Look for group name in current level
	if group, exists := data[groupName]; exists {
		if groupMap, ok := group.(map[string]interface{}); ok {
			return groupMap, true
		}
		return nil, false
	}

	// If not found at current level, search recursively
	for _, value := range data {
		if nestedMap, ok := value.(map[string]interface{}); ok {
			if result, found := ExtractGroup(nestedMap, groupName); found {
				return result, true
			}
		}
	}

	return nil, false
}

// GetDataField extracts a specific field from a map[string]interface{} with existence check
func GetDataField(data map[string]interface{}, key string) (interface{}, bool) {
	if data == nil {
		return nil, false
	}
	value, exists := data[key]
	return value, exists
}

// GetDataFieldAsString extracts a specific field from a map[string]interface{} and returns it as string
func GetDataFieldAsString(data map[string]interface{}, key string) (string, bool) {
	if data == nil {
		return "", false
	}
	value, exists := data[key]
	if !exists {
		return "", false
	}
	if str, ok := value.(string); ok {
		return str, true
	}
	return fmt.Sprintf("%v", value), true
}

// GetDataFieldAsInt extracts a specific field from a map[string]interface{} and returns it as int64
func GetDataFieldAsInt(data map[string]interface{}, key string) (int64, bool) {
	if data == nil {
		return 0, false
	}
	value, exists := data[key]
	if !exists {
		return 0, false
	}

	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// GetDataFieldAsFloat extracts a specific field from a map[string]interface{} and returns it as float64
func GetDataFieldAsFloat(data map[string]interface{}, key string) (float64, bool) {
	if data == nil {
		return 0, false
	}
	value, exists := data[key]
	if !exists {
		return 0, false
	}

	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// GetDataFieldAsBool extracts a specific field from a map[string]interface{} and returns it as bool
func GetDataFieldAsBool(data map[string]interface{}, key string) (bool, bool) {
	if data == nil {
		return false, false
	}
	value, exists := data[key]
	if !exists {
		return false, false
	}

	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b, true
		}
	}
	return false, false
}

// GetGroupField extracts a field from within a slog.Group
// This combines ExtractGroup and GetDataField for convenience
func GetGroupField(data map[string]interface{}, groupName, fieldName string) (interface{}, bool) {
	group, found := ExtractGroup(data, groupName)
	if !found {
		return nil, false
	}
	return GetDataField(group, fieldName)
}

// GetGroupFieldAsString extracts a field from within a slog.Group and returns it as string
func GetGroupFieldAsString(data map[string]interface{}, groupName, fieldName string) (string, bool) {
	value, found := GetGroupField(data, groupName, fieldName)
	if !found {
		return "", false
	}
	if str, ok := value.(string); ok {
		return str, true
	}
	return fmt.Sprintf("%v", value), true
}

// GetGroupFieldAsInt extracts a field from within a slog.Group and returns it as int64
func GetGroupFieldAsInt(data map[string]interface{}, groupName, fieldName string) (int64, bool) {
	group, found := ExtractGroup(data, groupName)
	if !found {
		return 0, false
	}
	return GetDataFieldAsInt(group, fieldName)
}

// GetGroupFieldAsFloat extracts a field from within a slog.Group and returns it as float64
func GetGroupFieldAsFloat(data map[string]interface{}, groupName, fieldName string) (float64, bool) {
	group, found := ExtractGroup(data, groupName)
	if !found {
		return 0, false
	}
	return GetDataFieldAsFloat(group, fieldName)
}

// GetGroupFieldAsBool extracts a field from within a slog.Group and returns it as bool
func GetGroupFieldAsBool(data map[string]interface{}, groupName, fieldName string) (bool, bool) {
	group, found := ExtractGroup(data, groupName)
	if !found {
		return false, false
	}
	return GetDataFieldAsBool(group, fieldName)
}
