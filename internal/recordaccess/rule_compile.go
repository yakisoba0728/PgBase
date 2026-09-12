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
	directRequest    bool
	requestFallback  bool
}

func compileRecordRule(catalog schema.Catalog, collection schema.Collection, rule accessrule.Rule, principal identity.Subject, options ruleCompileOptions) (string, []any, error) {
	if rule.PocketBase != nil {
		resolver, err := accessrule.NewCatalogResolver(catalog, collection, collection.TableName)
		if err != nil {
			return "", nil, err
		}
		return accessrule.CompilePocketBase(rule, accessrule.PBCompileContext{
			Resolver: resolver, Principal: principal, Request: options.request, ArgOffset: options.argOffset,
		})
	}
	if options.requestFallback {
		predicate, args, err := accessrule.Compile(rule, collection, collection.TableName, principal, options.argOffset, false)
		if err == nil {
			return predicate, args, nil
		}
		request := accessrule.RequestContext{Present: map[string]bool{}, Changed: map[string]bool{}}
		if options.request != nil {
			request = *options.request
		}
		return accessrule.CompileWithRequest(rule, collection, collection.TableName, principal, options.argOffset, options.prospectiveOwner, request)
	}
	if options.directRequest && options.request != nil {
		return accessrule.CompileWithRequest(rule, collection, collection.TableName, principal, options.argOffset, options.prospectiveOwner, *options.request)
	}
	return accessrule.Compile(rule, collection, collection.TableName, principal, options.argOffset, options.prospectiveOwner)
}
