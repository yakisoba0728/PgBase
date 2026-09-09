package selfupdate

import (
	"strconv"
	"strings"
)

type version struct {
	major, minor, patch uint64
	pre                 []string
}

func parseVersion(raw string) (version, bool) {
	if !strings.HasPrefix(raw, "v") || strings.Contains(raw, "+") {
		return version{}, false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "v"), "-")
	if len(parts) > 2 {
		return version{}, false
	}
	main := strings.Split(parts[0], ".")
	if len(main) != 3 {
		return version{}, false
	}
	values := [3]uint64{}
	for index, part := range main {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return version{}, false
		}
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version{}, false
		}
		values[index] = parsed
	}
	result := version{major: values[0], minor: values[1], patch: values[2]}
	if len(parts) == 2 {
		if parts[1] == "" {
			return version{}, false
		}
		result.pre = strings.Split(parts[1], ".")
		for _, identifier := range result.pre {
			if identifier == "" || (len(identifier) > 1 && identifier[0] == '0' && allDigits(identifier)) {
				return version{}, false
			}
			for _, character := range identifier {
				if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
					return version{}, false
				}
			}
		}
	}
	return result, true
}

func validVersion(raw string) bool { _, ok := parseVersion(raw); return ok }

func allDigits(raw string) bool {
	for _, character := range raw {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func compareVersion(left, right version) int {
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.pre) == 0 && len(right.pre) != 0 {
		return 1
	}
	if len(left.pre) != 0 && len(right.pre) == 0 {
		return -1
	}
	for index := 0; index < len(left.pre) && index < len(right.pre); index++ {
		leftID, rightID := left.pre[index], right.pre[index]
		leftNumber, rightNumber := allDigits(leftID), allDigits(rightID)
		if leftNumber && rightNumber {
			leftValue, _ := strconv.ParseUint(leftID, 10, 64)
			rightValue, _ := strconv.ParseUint(rightID, 10, 64)
			if leftValue < rightValue {
				return -1
			}
			if leftValue > rightValue {
				return 1
			}
			continue
		}
		if leftNumber != rightNumber {
			if leftNumber {
				return -1
			}
			return 1
		}
		if leftID < rightID {
			return -1
		}
		if leftID > rightID {
			return 1
		}
	}
	if len(left.pre) < len(right.pre) {
		return -1
	}
	if len(left.pre) > len(right.pre) {
		return 1
	}
	return 0
}
