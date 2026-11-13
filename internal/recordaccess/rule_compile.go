package recordaccess

import (
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type ruleCompileOptions struct {
	request          *accessrule.RequestContext
	argOffset        int
	prospectiveOwner bool
}

func compileRecordRule(catalog schema.Catalog, collection schema.Collection, rule accessrule.Rule, principal identity.Subject, options ruleCompileOptions) (string, []any, error) {
	return accessrule.CompilePolicy(rule, accessrule.PolicyContext{
		Catalog: catalog, Collection: collection, Alias: collection.TableName, Principal: principal,
		Request: options.request, ArgOffset: options.argOffset, ProspectiveOwner: options.prospectiveOwner,
	})
}
