package main

import (
	"errors"
	"html"
	"strings"
	"time"
)

func processHistogram(container interface{}) interface{} {
	if data, ok := container.([]interface{}); ok && len(data) > 5 {
		return []int{
			toInt(data[1]),
			toInt(data[2]),
			toInt(data[3]),
			toInt(data[4]),
			toInt(data[5]),
		}
	}
	return []int{0, 0, 0, 0, 0}
}

func processPrice(price interface{}) interface{} {
	if p, ok := price.(float64); ok {
		return p / 1000000
	}
	return 0
}

func processFreeFlag(flag interface{}) interface{} {
	return flag == 0
}

func datetimeFromTimestamp(val interface{}) any {
	ts, ok := val.(float64)
	if !ok {
		return ""
	}
	return time.Unix(int64(ts), 0).Format(time.RFC3339)
}

func toInt(val interface{}) int {
	if v, ok := val.(float64); ok {
		return int(v)
	}
	return 0
}

func ptr(i int) *int {
	return &i
}

func unescapeText(s interface{}) interface{} {
	return html.UnescapeString(strings.ReplaceAll(s.(string), "<br>", "\r\n"))
}

func nestedLookup(source map[int]interface{}, indexes []int) (interface{}, error) {
	if len(indexes) == 0 {
		return nil, errors.New("indexes cannot be empty")
	}

	current, exists := source[indexes[0]]
	if !exists {
		return nil, errors.New("key not found")
	}

	if len(indexes) == 1 {
		return current, nil
	}

	nextSource := make(map[int]interface{})
	for i := 0; i < len(current.([]interface{})); i++ {
		nextSource[i] = current.([]interface{})[i]
	}

	return nestedLookup(nextSource, indexes[1:])
}

func (e *ElementSpec) ExtractContent(source map[int]interface{}) interface{} {
	var result interface{}
	var err error

	defer func() {
		if r := recover(); r != nil {
			result = e.FallbackValue
		}
	}()

	if e.DsNum == nil {
		result, err = nestedLookup(source, e.DataMap)
	} else {
		dsSource, exists := source[*e.DsNum]

		if !exists {
			return e.FallbackValue
		}

		dsMap := make(map[int]interface{})
		for i := 0; i < len(dsSource.([]interface{})); i++ {
			dsMap[i] = dsSource.([]interface{})[i]
		}

		result, err = nestedLookup(dsMap, e.DataMap)
	}

	if err != nil {
		if fallbackSpec, ok := e.FallbackValue.(*ElementSpec); ok {
			return fallbackSpec.ExtractContent(source)
		}
		return e.FallbackValue
	}

	if e.PostProcessor != nil {
		result = e.PostProcessor(result)
	}

	return result
}
