package entities

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// ============================ JSON 列类型 ============================

// JSONStringArray 存字符串数组的列（如游戏的 learn / about）。
type JSONStringArray []string

// Value 实现 driver.Valuer
func (a JSONStringArray) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(a))
	if err != nil {
		return nil, fmt.Errorf("marshal JSONStringArray: %w", err)
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner
func (a *JSONStringArray) Scan(src interface{}) error {
	if src == nil {
		*a = JSONStringArray{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("JSONStringArray: unsupported type %T", src)
	}
	if len(b) == 0 {
		*a = JSONStringArray{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("unmarshal JSONStringArray: %w", err)
	}
	*a = JSONStringArray(out)
	return nil
}

// JSONIntArray 存整数数组的列（如签到已领取的阶梯阈值）
type JSONIntArray []int

func (a JSONIntArray) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]int(a))
	if err != nil {
		return nil, fmt.Errorf("marshal JSONIntArray: %w", err)
	}
	return string(b), nil
}

func (a *JSONIntArray) Scan(src interface{}) error {
	if src == nil {
		*a = JSONIntArray{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("JSONIntArray: unsupported type %T", src)
	}
	if len(b) == 0 {
		*a = JSONIntArray{}
		return nil
	}
	var out []int
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("unmarshal JSONIntArray: %w", err)
	}
	*a = JSONIntArray(out)
	return nil
}

// Shot 游戏截图
type Shot struct {
	Src     string `json:"src"`
	Caption string `json:"caption"`
}

// JSONShotList 存截图数组的列
type JSONShotList []Shot

func (s JSONShotList) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]Shot(s))
	if err != nil {
		return nil, fmt.Errorf("marshal JSONShotList: %w", err)
	}
	return string(b), nil
}

func (s *JSONShotList) Scan(src interface{}) error {
	if src == nil {
		*s = JSONShotList{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("JSONShotList: unsupported type %T", src)
	}
	if len(b) == 0 {
		*s = JSONShotList{}
		return nil
	}
	var out []Shot
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("unmarshal JSONShotList: %w", err)
	}
	*s = JSONShotList(out)
	return nil
}
