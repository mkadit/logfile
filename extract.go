// logfile/extract.go - Data extraction utilities
package logfile

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// LogData represents the structure of captured JSON logs
type LogData struct {
	EventType string       `json:"event_type"`
	Timestamp string       `json:"timestamp"`
	Level     *string      `json:"level,omitempty"`
	Msg       string       `json:"msg"`
	Error     *string      `json:"error,omitempty"`
	Data      *interface{} `json:"data,omitempty"`
}

// ParseLogJSON parses captured JSON string into LogData struct
func ParseLogJSON(jsonStr string) (*LogData, error) {
	var logData LogData
	if err := json.Unmarshal([]byte(jsonStr), &logData); err != nil {
		return nil, fmt.Errorf("failed to parse log JSON: %w", err)
	}
	return &logData, nil
}

// ExtractData extracts the data field from parsed log as map[string]interface{}
func ExtractData(logData *LogData) (map[string]interface{}, bool) {
	if logData == nil || logData.Data == nil {
		return nil, false
	}

	if data, ok := (*logData.Data).(map[string]interface{}); ok {
		return data, true
	}

	// If data is not a map, try to handle it
	return make(map[string]interface{}), false
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

// ExtractGroup extracts a slog.Group by name from the data field
// slog.Group creates nested structures, so we navigate to find the requested group
func ExtractGroup(data map[string]interface{}, groupName string) (map[string]interface{}, bool) {
	if data == nil {
		return nil, false
	}

	// Look for the group name in the current level
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
