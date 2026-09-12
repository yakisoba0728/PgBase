package schema

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
)

const (
	defaultFileSize   int64 = 5 << 20
	defaultEditorSize int64 = 5 << 20
	maximumFileSize   int64 = 100 << 20
	maximumFiles            = 100
)

func editorMaximumSize(options *FieldOptions) int64 {
	if options != nil && options.MaxSize != nil && *options.MaxSize > 0 {
		return *options.MaxSize
	}
	return defaultEditorSize
}

var thumbPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,3})x(?:0|[1-9][0-9]{0,3})[tbf]?$`)
var mimePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/(?:\*|[a-z0-9][a-z0-9!#$&^_.+-]*)$`)

func validateFieldOptions(kind FieldType, options *FieldOptions) error {
	if err := validateAdvancedFieldOptions(kind, options); err != nil {
		return err
	}
	if kind == TypeEditor || kind == TypeGeoPoint || kind == TypeAutoDate || kind == TypePassword {
		return nil
	}
	if kind != TypeFile {
		if options != nil && (options.MaxFileSize != nil || len(options.MimeTypes) != 0 || len(options.Thumbs) != 0 || options.Protected != nil) {
			return errors.New("file options require file type")
		}
		if kind == TypeTimestampTZ {
			if options == nil {
				return nil
			}
			for _, bound := range []*string{options.Min, options.Max} {
				if bound != nil {
					if parsed, err := time.Parse(time.RFC3339Nano, *bound); err != nil || parsed.Location() == nil {
						return errors.New("invalid date bound")
					}
				}
			}
			copy := *options
			copy.Min, copy.Max = nil, nil
			return fieldvalue.ValidateDefinition(string(kind), fieldInputValueOptions(&copy))
		}
		return fieldvalue.ValidateDefinition(string(kind), fieldInputValueOptions(options))
	}
	if options == nil {
		return nil
	}
	if options.MinLength != nil || options.MaxLength != nil || options.Pattern != "" || options.Format != "" || len(options.AllowedDomains) != 0 || len(options.DeniedDomains) != 0 || options.AutogeneratePattern != "" || len(options.AllowedValues) != 0 || options.Min != nil || options.Max != nil || options.MinSelect != nil || options.MaxSize != nil {
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

func validateAdvancedFieldOptions(kind FieldType, options *FieldOptions) error {
	if options == nil {
		if kind == TypeAutoDate {
			return errors.New("autodate requires onCreate or onUpdate")
		}
		return nil
	}
	hasGeneric := options.MinLength != nil || options.MaxLength != nil || options.Pattern != "" || options.Format != "" || len(options.AllowedDomains) != 0 || len(options.DeniedDomains) != 0 || options.AutogeneratePattern != "" || len(options.AllowedValues) != 0 || options.MinSelect != nil || options.MaxSelect != nil || options.Min != nil || options.Max != nil || options.MaxFileSize != nil || len(options.MimeTypes) != 0 || len(options.Thumbs) != 0
	switch kind {
	case TypeEditor:
		if hasGeneric || options.OnCreate || options.OnUpdate || options.MaxSize != nil && (*options.MaxSize < 0 || *options.MaxSize > MaximumRecordBytes) {
			return errors.New("invalid editor options")
		}
	case TypeGeoPoint:
		if hasGeneric || options.MaxSize != nil || options.ConvertURLs || options.OnCreate || options.OnUpdate {
			return errors.New("invalid geoPoint options")
		}
	case TypeAutoDate:
		if hasGeneric || options.MaxSize != nil || options.ConvertURLs || !options.OnCreate && !options.OnUpdate {
			return errors.New("invalid autodate options")
		}
	case TypePassword:
		if options.Format != "" || len(options.AllowedDomains) != 0 || len(options.DeniedDomains) != 0 || options.AutogeneratePattern != "" || len(options.AllowedValues) != 0 || options.MinSelect != nil || options.MaxSelect != nil || options.Min != nil || options.Max != nil || options.MaxFileSize != nil || len(options.MimeTypes) != 0 || len(options.Thumbs) != 0 || options.MaxSize != nil || options.ConvertURLs || options.OnCreate || options.OnUpdate {
			return errors.New("invalid password options")
		}
		return fieldvalue.ValidateDefinition("text", fieldInputValueOptions(options))
	default:
		if options.MaxSize != nil && kind != TypeJSONB || options.ConvertURLs || options.OnCreate || options.OnUpdate {
			return errors.New("advanced options require matching field type")
		}
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
