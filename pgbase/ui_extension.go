package pgbase

import (
	"fmt"
	"io/fs"
)

// UIExtension is an operator-installed administrator UI contribution. Name
// identifies its assets at /admin/extensions/{name}/ and FS may contain an
// optional main.js. Registration is accepted only through Options at process
// startup; JavaScript routes and browser code cannot add an extension or grant
// themselves administrator capabilities.
type UIExtension struct {
	Name string
	FS   fs.FS
}

func validateUIExtensions(extensions []UIExtension) error {
	seen := make(map[string]struct{}, len(extensions))
	for index, extension := range extensions {
		if !validUIExtensionName(extension.Name) {
			return fmt.Errorf("UIExtensions[%d].Name: must contain 1 to 64 ASCII letters, digits, spaces, _ or - and start with a letter or digit", index)
		}
		if extension.FS == nil {
			return fmt.Errorf("UIExtensions[%d].FS: must not be nil", index)
		}
		if _, duplicate := seen[extension.Name]; duplicate {
			return fmt.Errorf("UIExtensions[%d].Name: duplicate %q", index, extension.Name)
		}
		seen[extension.Name] = struct{}{}
	}
	return nil
}

func validUIExtensionName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for index, character := range []byte(name) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		if (index == 0 && !letter && !digit) || (!letter && !digit && character != ' ' && character != '_' && character != '-') {
			return false
		}
	}
	return true
}
