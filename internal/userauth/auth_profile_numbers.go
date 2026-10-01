package userauth

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"

	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/jsonvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func validAuthNumeric(value any, kind schema.FieldType, options fieldvalue.Options) bool {
	var text string
	switch number := value.(type) {
	case json.Number:
		text = number.String()
	case float64:
		text = jsonNumber(number)
	default:
		return false
	}
	if !jsonvalue.ValidPostgresNumber(text) || fieldvalue.ValidateNumeric(text, options) != "" {
		return false
	}
	if kind == schema.TypeNumeric {
		return true
	}
	number, ok := new(big.Rat).SetString(text)
	if !ok || !number.IsInt() || !number.Num().IsInt64() {
		return false
	}
	integer := number.Num().Int64()
	return kind == schema.TypeBigInt || integer >= math.MinInt32 && integer <= math.MaxInt32
}

func authCoordinate(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case json.Number:
		coordinate, err := number.Float64()
		return coordinate, err == nil && !math.IsNaN(coordinate) && !math.IsInf(coordinate, 0)
	}
	return 0, false
}

// Stored JSON uses json.Number while existing Go/HTTP patch callers may use
// float64. Compare numeric values without changing :changed rule semantics.
func authRuleValuesEqual(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	if leftErr == nil && rightErr == nil && jsonvalue.ValidPostgresNumber(string(leftRaw)) && jsonvalue.ValidPostgresNumber(string(rightRaw)) {
		leftNumber, leftOK := new(big.Rat).SetString(string(leftRaw))
		rightNumber, rightOK := new(big.Rat).SetString(string(rightRaw))
		return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
	}
	switch value := left.(type) {
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, item := range value {
			otherItem, exists := other[key]
			if !exists || !authRuleValuesEqual(item, otherItem) {
				return false
			}
		}
		return true
	case []any:
		other, ok := right.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for i := range value {
			if !authRuleValuesEqual(value[i], other[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}
