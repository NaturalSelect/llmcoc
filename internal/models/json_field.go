package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSONField is a generic GORM type that serializes T to/from JSON text column.
type JSONField[T any] struct {
	Data T
}

func (j JSONField[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.Data)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (j *JSONField[T]) Scan(src any) error {
	var bytes []byte
	switch v := src.(type) {
	case string:
		bytes = []byte(v)
	case []byte:
		bytes = v
	default:
		return fmt.Errorf("unsupported type %T", src)
	}
	return json.Unmarshal(bytes, &j.Data)
}

func (j JSONField[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(j.Data)
}

func (j *JSONField[T]) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &j.Data)
}
