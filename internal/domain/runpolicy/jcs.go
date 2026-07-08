package runpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func canonicalizeJCS(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "null", nil
	case string:
		return jsonString(v)
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("JCS cannot canonicalize non-finite numbers")
		}
		return encodeJSON(v)
	case []string:
		items := make([]string, 0, len(v))
		for _, item := range v {
			encoded, err := canonicalizeJCS(item)
			if err != nil {
				return "", err
			}
			items = append(items, encoded)
		}
		return "[" + strings.Join(items, ",") + "]", nil
	case []map[string]any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			encoded, err := canonicalizeJCS(item)
			if err != nil {
				return "", err
			}
			items = append(items, encoded)
		}
		return "[" + strings.Join(items, ",") + "]", nil
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			encoded, err := canonicalizeJCS(item)
			if err != nil {
				return "", err
			}
			items = append(items, encoded)
		}
		return "[" + strings.Join(items, ",") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		entries := make([]string, 0, len(keys))
		for _, key := range keys {
			encodedKey, err := jsonString(key)
			if err != nil {
				return "", err
			}
			encodedValue, err := canonicalizeJCS(v[key])
			if err != nil {
				return "", err
			}
			entries = append(entries, encodedKey+":"+encodedValue)
		}
		return "{" + strings.Join(entries, ",") + "}", nil
	default:
		rv := reflect.ValueOf(value)
		if rv.Kind() == reflect.Slice {
			items := make([]string, 0, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				encoded, err := canonicalizeJCS(rv.Index(i).Interface())
				if err != nil {
					return "", err
				}
				items = append(items, encoded)
			}
			return "[" + strings.Join(items, ",") + "]", nil
		}
		return "", fmt.Errorf("JCS cannot canonicalize %T", value)
	}
}

func jsonString(value string) (string, error) {
	return encodeJSON(value)
}

func encodeJSON(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
