package responseprojection

// PocketBaseRecord adds PocketBase record identity and timestamp aliases to a
// caller-owned shallow copy. Native PgBase keys and UUID values remain intact.
func PocketBaseRecord(record map[string]any, collectionID, collectionName string) map[string]any {
	result := make(map[string]any, len(record)+4)
	for key, value := range record {
		result[key] = value
	}
	result["collectionId"] = collectionID
	result["collectionName"] = collectionName
	if value, ok := record["created_at"]; ok {
		result["created"] = value
	}
	if value, ok := record["updated_at"]; ok {
		result["updated"] = value
	}
	return result
}
