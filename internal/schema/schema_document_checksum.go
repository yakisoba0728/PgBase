package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type sealedSchemaImportPlan struct {
	Operation   string   `json:"operation"`
	BaseVersion int64    `json:"baseVersion"`
	Statements  []string `json:"statements"`
}

func cloneSchemaDocument(document SchemaDocument) SchemaDocument {
	raw, _ := json.Marshal(document)
	var cloned SchemaDocument
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func schemaDocumentChecksum(version int64, policyChecksum string, document SchemaDocument, plans []Plan) (string, error) {
	sealedPlans := make([]sealedSchemaImportPlan, len(plans))
	for i, plan := range plans {
		// Projected catalog IDs are allocated while planning and are deliberately
		// excluded. The ordered operation and SQL are the executable review contract.
		sealedPlans[i] = sealedSchemaImportPlan{Operation: plan.Operation, BaseVersion: plan.BaseVersion, Statements: plan.Statements}
	}
	raw, err := json.Marshal(struct {
		Version        int64                    `json:"version"`
		PolicyChecksum string                   `json:"policyChecksum"`
		Document       SchemaDocument           `json:"document"`
		Plans          []sealedSchemaImportPlan `json:"plans"`
	}{version, policyChecksum, document, sealedPlans})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
