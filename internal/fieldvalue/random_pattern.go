package fieldvalue

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// ValidateRandomPattern accepts only regexp operations for which
// RandomStringByPattern can construct a value directly. The maximum byte bound
// prevents an autogenerate expression from bypassing the record text budget.
func ValidateRandomPattern(pattern string, maximumBytes int) error {
	if len(pattern) == 0 || len(pattern) > 1024 {
		return errors.New("invalid pattern size")
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return err
	}
	minimum, maximum, err := randomPatternBounds(parsed)
	if err != nil {
		return err
	}
	if maximumBytes < 1 || maximum < 1 || minimum > maximum || maximum > maximumBytes {
		return errors.New("generated value exceeds bounds")
	}
	return nil
}

// RandomStringByPattern generates one cryptographically random value from the
// bounded, validated regexp subset. The returned value always matches pattern.
func RandomStringByPattern(reader io.Reader, pattern string) (string, error) {
	if reader == nil {
		return "", errors.New("random reader is required")
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	if err := writeRandomPattern(reader, parsed, &result); err != nil {
		return "", err
	}
	return result.String(), nil
}

func writeRandomPattern(reader io.Reader, expression *syntax.Regexp, result *strings.Builder) error {
	switch expression.Op {
	case syntax.OpEmptyMatch:
		return nil
	case syntax.OpLiteral:
		_, err := result.WriteString(string(expression.Rune))
		return err
	case syntax.OpCharClass:
		value, err := randomRune(reader, expression.Rune)
		if err == nil {
			_, err = result.WriteRune(value)
		}
		return err
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		value, err := randomRune(reader, []rune{'0', '9', 'A', 'Z', 'a', 'z'})
		if err == nil {
			_, err = result.WriteRune(value)
		}
		return err
	case syntax.OpCapture:
		return writeRandomPattern(reader, expression.Sub[0], result)
	case syntax.OpConcat:
		for _, child := range expression.Sub {
			if err := writeRandomPattern(reader, child, result); err != nil {
				return err
			}
		}
		return nil
	case syntax.OpAlternate:
		index, err := randomIndex(reader, len(expression.Sub))
		if err != nil {
			return err
		}
		return writeRandomPattern(reader, expression.Sub[index], result)
	case syntax.OpQuest:
		return writeRandomRepeat(reader, expression.Sub[0], result, 0, 1)
	case syntax.OpRepeat:
		if expression.Max < 0 {
			return errors.New("unbounded pattern repetition")
		}
		return writeRandomRepeat(reader, expression.Sub[0], result, expression.Min, expression.Max)
	case syntax.OpStar, syntax.OpPlus:
		return errors.New("unbounded pattern repetition")
	default:
		return fmt.Errorf("unsupported pattern operation %d", expression.Op)
	}
}

func writeRandomRepeat(reader io.Reader, child *syntax.Regexp, result *strings.Builder, minimum, maximum int) error {
	count := minimum
	if maximum > minimum {
		offset, err := randomIndex(reader, maximum-minimum+1)
		if err != nil {
			return err
		}
		count += offset
	}
	for range count {
		if err := writeRandomPattern(reader, child, result); err != nil {
			return err
		}
	}
	return nil
}

func randomRune(reader io.Reader, pairs []rune) (rune, error) {
	if len(pairs) == 0 || len(pairs)%2 != 0 {
		return 0, errors.New("invalid character class")
	}
	total := int64(0)
	for index := 0; index < len(pairs); index += 2 {
		if pairs[index] > pairs[index+1] {
			return 0, errors.New("invalid character range")
		}
		total += int64(pairs[index+1]-pairs[index]) + 1
	}
	choice, err := randomBigInt(reader, total)
	if err != nil {
		return 0, err
	}
	for index := 0; index < len(pairs); index += 2 {
		size := int64(pairs[index+1]-pairs[index]) + 1
		if choice < size {
			return pairs[index] + rune(choice), nil
		}
		choice -= size
	}
	return 0, errors.New("random character selection failed")
}

func randomIndex(reader io.Reader, size int) (int, error) {
	if size < 1 {
		return 0, errors.New("empty random choice")
	}
	value, err := randomBigInt(reader, int64(size))
	return int(value), err
}

func randomBigInt(reader io.Reader, maximum int64) (int64, error) {
	value, err := rand.Int(reader, big.NewInt(maximum))
	if err != nil {
		return 0, err
	}
	return value.Int64(), nil
}

func randomPatternBounds(expression *syntax.Regexp) (int, int, error) {
	switch expression.Op {
	case syntax.OpEmptyMatch:
		return 0, 0, nil
	case syntax.OpLiteral:
		bytes := 0
		for _, value := range expression.Rune {
			bytes += utf8.RuneLen(value)
		}
		return bytes, bytes, nil
	case syntax.OpCharClass:
		maximum := 1
		for _, value := range expression.Rune {
			if size := utf8.RuneLen(value); size > maximum {
				maximum = size
			}
		}
		return 1, maximum, nil
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1, 1, nil
	case syntax.OpCapture:
		return randomPatternBounds(expression.Sub[0])
	case syntax.OpConcat:
		minimum, maximum := 0, 0
		for _, child := range expression.Sub {
			childMinimum, childMaximum, err := randomPatternBounds(child)
			if err != nil {
				return 0, 0, err
			}
			minimum += childMinimum
			maximum += childMaximum
		}
		return minimum, maximum, nil
	case syntax.OpAlternate:
		minimum, maximum := -1, 0
		for _, child := range expression.Sub {
			childMinimum, childMaximum, err := randomPatternBounds(child)
			if err != nil {
				return 0, 0, err
			}
			if minimum < 0 || childMinimum < minimum {
				minimum = childMinimum
			}
			if childMaximum > maximum {
				maximum = childMaximum
			}
		}
		return minimum, maximum, nil
	case syntax.OpQuest, syntax.OpRepeat:
		childMinimum, childMaximum, err := randomPatternBounds(expression.Sub[0])
		if err != nil {
			return 0, 0, err
		}
		minimum, maximum := expression.Min, expression.Max
		switch expression.Op {
		case syntax.OpQuest:
			minimum, maximum = 0, 1
		case syntax.OpRepeat:
			if maximum < 0 {
				return 0, 0, errors.New("unbounded pattern repetition")
			}
		}
		return childMinimum * minimum, childMaximum * maximum, nil
	case syntax.OpStar, syntax.OpPlus:
		return 0, 0, errors.New("unbounded pattern repetition")
	default:
		return 0, 0, fmt.Errorf("unsupported pattern operation %d", expression.Op)
	}
}
