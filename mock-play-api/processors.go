// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"time"
)

func datetimeFromTimestamp(val interface{}) any {
	ts, ok := val.(float64)
	if !ok {
		return ""
	}
	return time.Unix(int64(ts), 0).Format(time.RFC3339)
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
