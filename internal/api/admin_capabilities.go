package api

// Capabilities describe registered administrator surfaces, not merely the
// presence of a frontend adapter. No probing writes or database calls occur.
func administratorCapabilities(d Dependencies) map[string]bool {
	return map[string]bool{
		"settings":           d.RuntimeSettings != nil,
		"profile":            len(adminProfileRoutes(d)) != 0,
		"administrators":     len(adminAccountRoutes(d)) != 0,
		"schemaTransfer":     len(schemaDocumentRoutes(d)) != 0,
		"authCollections":    d.AuthCollections != nil,
		"authAdministration": d.AuthAdministration != nil,
		"users":              d.UserAuth != nil,
		"serviceTokens":      d.ServiceTokens != nil,
		"storage":            d.StorageAdministration != nil,
		"storageMigrations":  d.StorageMigrations != nil,
		"backupStorage":      d.BackupStorageAdministration != nil,
		"backups":            d.Backups != nil,
		"logs":               d.RequestLog != nil,
		"jobs":               d.Jobs != nil,
		"realtime":           len(realtimeOperationRoutes(d)) != 0,
		"files":              d.Files != nil,
		"rules":              d.RecordRules != nil,
	}
}
