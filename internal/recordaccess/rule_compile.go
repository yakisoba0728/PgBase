package recordaccess

import (
	"strings"

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
	if recordRuleRequiresRequestMetadata(rule) && (options.request == nil || options.request.Method == "") {
		// Mutation body metadata exists even for embedded calls. It must not
		// make absent HTTP metadata satisfy a negative predicate or an OR arm.
		return "FALSE", nil, nil
	}
	return accessrule.CompilePolicy(rule, accessrule.PolicyContext{
		Catalog: catalog, Collection: collection, Alias: collection.TableName, Principal: principal,
		Request: options.request, ArgOffset: options.argOffset, ProspectiveOwner: options.prospectiveOwner,
	})
}

func recordRuleRequiresRequestMetadata(rule accessrule.Rule) bool {
	requires := func(path string) bool {
		return strings.HasPrefix(path, "__pgbase_request_") &&
			!strings.HasPrefix(path, "__pgbase_request_auth_") &&
			!strings.HasPrefix(path, "__pgbase_request_body_")
	}
	if requires(rule.Field) || requires(rule.OtherField) {
		return true
	}
	if rule.Function != nil {
		for _, arg := range rule.Function.Args {
			if requires(arg.Field) {
				return true
			}
		}
	}
	for _, arg := range rule.Args {
		if recordRuleRequiresRequestMetadata(arg) {
			return true
		}
	}
	return false
}
