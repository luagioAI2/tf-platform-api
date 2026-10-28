package structure

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// 辅助函数：统一类型转换（避免重复代码）
func ToInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int8:
		return int64(val)
	case int16:
		return int64(val)
	case int32:
		return int64(val)
	case int64:
		return val
	default:
		return 0
	}
}

func ToUint64(v interface{}) uint64 {
	switch val := v.(type) {
	case uint:
		return uint64(val)
	case uint8:
		return uint64(val)
	case uint16:
		return uint64(val)
	case uint32:
		return uint64(val)
	case uint64:
		return val
	default:
		return 0
	}
}

func ToFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float32:
		return float64(val)
	case float64:
		return val
	default:
		return 0
	}
}

// jsonToStrMap 【封装后的核心函数】将map[string]interface{}转为map[string]string
func InterfaceMapToStrMap(jsonData map[string]interface{}) map[string]string {
	resMap := make(map[string]string, len(jsonData))
	for k, v := range jsonData {
		switch val := v.(type) {
		case string:
			resMap[k] = val
		case int, int8, int16, int32, int64:
			resMap[k] = strconv.FormatInt(ToInt64(val), 10)
		case uint, uint8, uint16, uint32, uint64:
			resMap[k] = strconv.FormatUint(ToUint64(val), 10)
		case float32, float64:
			resMap[k] = strconv.FormatFloat(ToFloat64(val), 'f', -1, 64)
		case bool:
			resMap[k] = strconv.FormatBool(val)
		case nil:
			resMap[k] = ""
		default:
			// 复杂类型转为JSON字符串（兼容数组、嵌套结构等）
			b, _ := json.Marshal(val)
			resMap[k] = string(b)
		}
	}
	return resMap
}

// mapToStruct 仅绑定map中与结构体字段匹配的键（简洁反射，只处理已知字段）
func StrMapToStruct(m map[string]string, s interface{}) error {
	val := reflect.ValueOf(s).Elem()
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldTyp := typ.Field(i)

		// 跳过内部字段（json:"-"）
		jsonTag := fieldTyp.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		// 获取JSON字段名（处理tag选项，如omitempty）
		jsonKey := jsonTag
		if idx := strings.Index(jsonKey, ","); idx != -1 {
			jsonKey = jsonKey[:idx]
		}
		if jsonKey == "" {
			jsonKey = fieldTyp.Name
		}

		// 从map获取值，不存在则跳过
		strVal, ok := m[jsonKey]
		if !ok || strVal == "" {
			continue
		}

		// 字段赋值（简洁类型转换）
		switch field.Kind() {
		case reflect.String:
			field.SetString(strVal)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			num, _ := strconv.ParseInt(strVal, 10, 64)
			field.SetInt(num)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			num, _ := strconv.ParseUint(strVal, 10, 64)
			field.SetUint(num)
		case reflect.Float32, reflect.Float64:
			num, _ := strconv.ParseFloat(strVal, 64)
			field.SetFloat(num)
		}
	}
	return nil
}
