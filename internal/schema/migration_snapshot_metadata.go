package schema

func migrationSnapshotPointer[T any](value T) *T { return &value }

func migrationSnapshotIndex(index Index) IndexInput {
	return indexInputFromCatalog(index)
}

// migrationSnapshotMatches treats metadata added after the original v1 shape
// as inherited when it is absent from an older snapshot. Presence still makes
// every value, including false and the empty compatibility name, significant.
func migrationSnapshotMatches(actualRaw, requestedRaw []byte) (bool, error) {
	actual, err := decodeMigrationSnapshot(actualRaw)
	if err != nil {
		return false, err
	}
	requested, err := decodeMigrationSnapshot(requestedRaw)
	if err != nil {
		return false, err
	}
	actualByName := migrationCollectionMap(actual)
	for collectionIndex := range requested.Collections {
		item := &requested.Collections[collectionIndex]
		fallback, ok := actualByName[item.Name]
		if !ok {
			continue
		}
		if item.CompatibilityName == nil {
			item.CompatibilityName = fallback.CompatibilityName
		}
		if item.Type == nil {
			item.Type = fallback.Type
		}
		if item.System == nil {
			item.System = fallback.System
		}
		if item.ValidationMode == nil {
			item.ValidationMode = fallback.ValidationMode
		}
		fallbackFields := migrationFieldMap(fallback.Fields)
		fallbackFieldsByName := make(map[string]FieldInput, len(fallback.Fields))
		for _, field := range fallback.Fields {
			fallbackFieldsByName[field.Name] = field
		}
		for fieldIndex := range item.Fields {
			field := &item.Fields[fieldIndex]
			if field.System != nil {
				continue
			}
			prior, exists := fallbackFieldsByName[field.Name]
			if field.ID != nil {
				if byID, found := fallbackFields[*field.ID]; found {
					prior, exists = byID, true
				}
			}
			if exists {
				field.System = prior.System
			}
		}
	}
	actualCanonical, err := MarshalMigrationSnapshot(actual)
	if err != nil {
		return false, err
	}
	requestedCanonical, err := MarshalMigrationSnapshot(requested)
	if err != nil {
		return false, err
	}
	return MigrationSnapshotsEquivalent(actualCanonical, requestedCanonical)
}
