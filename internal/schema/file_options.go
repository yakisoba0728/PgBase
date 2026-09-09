package schema

import (
	"errors"
	"regexp"
	"strings"

	"github.com/webisnoteasy/PgBase/internal/fieldvalue"
)

const (
	defaultFileSize int64 = 5 << 20
	maximumFileSize int64 = 100 << 20
	maximumFiles          = 100
)

var thumbPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,3})x(?:0|[1-9][0-9]{0,3})[tbf]?$`)
var mimePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/(?:\*|[a-z0-9][a-z0-9!#$&^_.+-]*)$`)

func validateFieldOptions(kind FieldType, options *FieldOptions) error {
	if kind != TypeFile {
		if options != nil && (options.MaxFileSize != nil || len(options.MimeTypes) != 0 || len(options.Thumbs) != 0) {
			return errors.New("file options require file type")
		}
		return fieldvalue.ValidateDefinition(string(kind), fieldInputValueOptions(options))
	}
	if options == nil {
		return nil
	}
	if options.MinLength != nil || options.MaxLength != nil || options.Pattern != "" || options.Format != "" || len(options.AllowedDomains) != 0 || len(options.AllowedValues) != 0 || options.Min != nil || options.Max != nil || options.MinSelect != nil {
		return fieldvalue.ValidateDefinition("file", fieldInputValueOptions(options))
	}
	if options.MaxSelect != nil && (*options.MaxSelect < 1 || *options.MaxSelect > maximumFiles) {
		return errInvalidFileOption{}
	}
	if options.MaxFileSize != nil && (*options.MaxFileSize < 1 || *options.MaxFileSize > maximumFileSize) {
		return errInvalidFileOption{}
	}
	seen := map[string]bool{}
	for _, value := range options.MimeTypes {
		value = strings.ToLower(strings.TrimSpace(value))
		if !mimePattern.MatchString(value) || seen[value] {
			return errInvalidFileOption{}
		}
		seen[value] = true
	}
	seen = map[string]bool{}
	for _, value := range options.Thumbs {
		if !thumbPattern.MatchString(value) || strings.HasPrefix(value, "0x0") || seen[value] {
			return errInvalidFileOption{}
		}
		seen[value] = true
	}
	return nil
}

type errInvalidFileOption struct{}

func (errInvalidFileOption) Error() string { return "invalid file option" }

func fileMaximumSize(options FieldOptions) int64 {
	if options.MaxFileSize != nil {
		return *options.MaxFileSize
	}
	return defaultFileSize
}

func fileMaximumCount(options FieldOptions) int {
	if options.MaxSelect != nil {
		return *options.MaxSelect
	}
	return 1
}
