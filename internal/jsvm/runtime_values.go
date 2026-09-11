package jsvm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"

	"github.com/dop251/goja"
)

var exactNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func exactValue(vm *goja.Runtime, value string) *goja.Object {
	object := vm.NewObject()
	_ = object.DefineDataProperty("__pgbaseExact", vm.ToValue(value), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	_ = object.Set("toString", func(goja.FunctionCall) goja.Value { return vm.ToValue(value) })
	return object
}

func decodeValue(vm *goja.Runtime, raw json.RawMessage, maximumDepth int) (goja.Value, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	return importValue(vm, decoded, 0, maximumDepth)
}

func importValue(vm *goja.Runtime, value any, depth, maximumDepth int) (goja.Value, error) {
	if depth > maximumDepth {
		return nil, errors.New("JSON nesting exceeds limit")
	}
	switch value := value.(type) {
	case nil:
		return goja.Null(), nil
	case bool, string:
		return vm.ToValue(value), nil
	case json.Number:
		return exactValue(vm, value.String()), nil
	case []any:
		array := vm.NewArray(len(value))
		for index, item := range value {
			converted, err := importValue(vm, item, depth+1, maximumDepth)
			if err != nil {
				return nil, err
			}
			if err := array.Set(fmt.Sprint(index), converted); err != nil {
				return nil, err
			}
		}
		return array, nil
	case map[string]any:
		object := vm.NewObject()
		for name, item := range value {
			converted, err := importValue(vm, item, depth+1, maximumDepth)
			if err != nil {
				return nil, err
			}
			if err := object.Set(name, converted); err != nil {
				return nil, err
			}
		}
		return object, nil
	default:
		return nil, errors.New("unsupported decoded JSON value")
	}
}

func encodeValue(value goja.Value, maximumDepth int) (json.RawMessage, error) {
	converted, err := exportValue(value, 0, maximumDepth, make(map[*goja.Object]bool))
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func exportValue(value goja.Value, depth, maximumDepth int, seen map[*goja.Object]bool) (any, error) {
	if depth > maximumDepth {
		return nil, errors.New("JSON nesting exceeds limit")
	}
	if goja.IsNull(value) {
		return nil, nil
	}
	if goja.IsUndefined(value) {
		if depth == 0 {
			return nil, nil
		}
		return nil, errors.New("undefined is not a JSON value")
	}
	switch exported := value.Export().(type) {
	case bool, string:
		return exported, nil
	case int64:
		if exported > 1<<53-1 || exported < -(1<<53-1) {
			return nil, errors.New("unsafe JavaScript integer must use pgbase.exact")
		}
		return exported, nil
	case float64:
		if math.IsNaN(exported) || math.IsInf(exported, 0) {
			return nil, errors.New("non-finite number is not JSON")
		}
		return exported, nil
	}
	object, ok := value.(*goja.Object)
	if !ok {
		return nil, errors.New("unsupported JavaScript value")
	}
	if marker := object.Get("__pgbaseExact"); marker != nil && !goja.IsUndefined(marker) {
		exact, ok := marker.Export().(string)
		if !ok || !exactNumberPattern.MatchString(exact) {
			return nil, errors.New("invalid exact number")
		}
		return json.Number(exact), nil
	}
	if seen[object] {
		return nil, errors.New("cyclic object is not JSON")
	}
	seen[object] = true
	defer delete(seen, object)
	if object.ClassName() == "Array" {
		length := object.Get("length").ToInteger()
		if length < 0 || length > 1_000_000 {
			return nil, errors.New("array length exceeds limit")
		}
		result := make([]any, int(length))
		for index := range result {
			converted, err := exportValue(object.Get(fmt.Sprint(index)), depth+1, maximumDepth, seen)
			if err != nil {
				return nil, err
			}
			result[index] = converted
		}
		return result, nil
	}
	if object.ClassName() != "Object" {
		return nil, fmt.Errorf("unsupported JavaScript object %s", object.ClassName())
	}
	result := make(map[string]any)
	for _, name := range object.Keys() {
		converted, err := exportValue(object.Get(name), depth+1, maximumDepth, seen)
		if err != nil {
			return nil, err
		}
		result[name] = converted
	}
	return result, nil
}
