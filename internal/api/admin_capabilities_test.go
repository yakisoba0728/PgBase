package api

import "testing"

func TestAdministratorCapabilitiesMatchRegisteredSurfaces(t *testing.T) {
	for _, d := range []Dependencies{completeDependencies(Dependencies{}), allOptionalRouteDependencies()} {
		routes := declareRoutes(d, false)
		patterns := map[string]bool{}
		for _, r := range routes {
			patterns[r.muxPattern()] = true
		}
		required := map[string]string{
			"settings": "GET /api/admin/settings", "profile": "GET /api/admin/profile", "administrators": "GET /api/admin/administrators",
			"schemaTransfer": "POST /api/admin/schema/import/preview", "authCollections": "GET /api/admin/auth-collections",
			"authAdministration": "GET /api/admin/auth-collections/{id}/administration", "users": "GET /api/admin/users",
			"serviceTokens": "POST /api/admin/service-tokens", "storage": "GET /api/admin/storage/files", "storageMigrations": "POST /api/admin/storage/files/migrations/preview",
			"backupStorage": "GET /api/admin/storage/backups", "backups": "GET /api/admin/backups", "logs": "GET /api/admin/logs", "jobs": "PUT /api/admin/jobs/{id}",
			"realtime": "GET /api/admin/realtime/status", "files": "GET /api/admin/files/{fileId}", "rules": "GET /api/admin/collections/{collection}/rules",
		}
		caps := administratorCapabilities(d)
		for key, pattern := range required {
			if caps[key] != patterns[pattern] {
				t.Errorf("capability %s=%v; route %s registered=%v", key, caps[key], pattern, patterns[pattern])
			}
		}
	}
}
