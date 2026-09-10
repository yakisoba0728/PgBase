package query

import (
	"encoding/json"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

// materializedMeasurement computes the exact flattened JSON size while
// memoizing shared targets. Capping arithmetic at maxBytes+1 prevents a
// branching relation graph from turning a small hydrated graph into an
// exponentially large allocation before the response limit is enforced.
type materializedMeasurement struct {
	maximum int
	cached  map[*materializedRecord]int
	active  map[*materializedRecord]struct{}
}

func newMaterializedMeasurement(maximum int) *materializedMeasurement {
	return &materializedMeasurement{
		maximum: maximum,
		cached:  make(map[*materializedRecord]int),
		active:  make(map[*materializedRecord]struct{}),
	}
}

func (m *materializedMeasurement) recordSize(record *materializedRecord) (int, error) {
	if record == nil {
		return 0, schema.ErrConflict
	}
	if size, ok := m.cached[record]; ok {
		return size, nil
	}
	if _, cycle := m.active[record]; cycle {
		return 0, schema.ErrConflict
	}
	m.active[record] = struct{}{}
	defer delete(m.active, record)

	encodedRoot, err := json.Marshal(record.root)
	if err != nil {
		return 0, err
	}
	size := m.cap(len(encodedRoot))
	expandSize, entries, err := m.expandSize(record.expand)
	if err != nil {
		return 0, err
	}
	if entries != 0 {
		if len(record.root) != 0 {
			size = m.add(size, 1) // comma before the sibling expand member
		}
		size = m.add(size, len(`"expand":`))
		size = m.add(size, expandSize)
	}
	m.cached[record] = size
	return size, nil
}

func (m *materializedMeasurement) expandSize(values map[string]materializedExpandValue) (int, int, error) {
	size := 2 // braces
	entries := 0
	for name, value := range values {
		var valueSize int
		if value.plural {
			valueSize = 2 // brackets
			for index, record := range value.many {
				childSize, err := m.recordSize(record)
				if err != nil {
					return 0, 0, err
				}
				if index != 0 {
					valueSize = m.add(valueSize, 1)
				}
				valueSize = m.add(valueSize, childSize)
			}
		} else {
			if value.single == nil {
				continue
			}
			var err error
			valueSize, err = m.recordSize(value.single)
			if err != nil {
				return 0, 0, err
			}
		}
		encodedName, err := json.Marshal(name)
		if err != nil {
			return 0, 0, err
		}
		if entries != 0 {
			size = m.add(size, 1)
		}
		size = m.add(size, len(encodedName)+1) // key plus colon
		size = m.add(size, valueSize)
		entries++
	}
	return size, entries, nil
}

func (m *materializedMeasurement) cap(value int) int {
	if m.maximum < 0 || value > m.maximum {
		return m.overflow()
	}
	return value
}

func (m *materializedMeasurement) add(left, right int) int {
	if left > m.maximum || right > m.maximum || right < 0 || left > m.maximum-right {
		return m.overflow()
	}
	return left + right
}

func (m *materializedMeasurement) overflow() int {
	if m.maximum < int(^uint(0)>>1) {
		return m.maximum + 1
	}
	return m.maximum
}
