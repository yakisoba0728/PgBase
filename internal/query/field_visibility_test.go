package query

import (
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
)

func TestHiddenFieldsAreProjectedAndRejectedForNonAdministrators(t *testing.T) {
	c := schema.Collection{Fields: []schema.Field{{Name: "title"}, {Name: "secret", Hidden: true}}}
	user := Principal{Kind: identity.User, ID: uuid.New()}
	record := map[string]any{"title": "shown", "secret": "private"}
	hideRecordFields(c, user, record)
	if _, found := record["secret"]; found {
		t.Fatal("hidden field remained")
	}
	adminRecord := map[string]any{"secret": "private"}
	hideRecordFields(c, Principal{Kind: identity.Administrator, ID: uuid.New()}, adminRecord)
	if adminRecord["secret"] != "private" {
		t.Fatal("administrator field removed")
	}
	if err := rejectHiddenQueryFields(c, user, filterComparison{field: "secret", operator: "~", value: filterLiteral{kind: filterLiteralString, text: "private"}}, nil); err == nil {
		t.Fatal("hidden filter accepted")
	}
	if err := rejectHiddenQueryFields(c, user, nil, []SortKey{{Field: "secret"}}); err == nil {
		t.Fatal("hidden sort accepted")
	}
}

func TestHiddenFieldsAreRemovedFromNestedExpands(t *testing.T) {
	user := Principal{Kind: identity.User, ID: uuid.New()}
	target := schema.Collection{Fields: []schema.Field{{Name: "label"}, {Name: "secret", Hidden: true}}}
	child := &materializedRecord{root: map[string]any{"label": "Ada", "secret": "private"}}
	root := &materializedRecord{root: map[string]any{"id": "root"}, expand: map[string]materializedExpandValue{"owner": {single: child}}}
	hideMaterializedFields(schema.Collection{}, []expandNode{{name: "owner", target: target}}, user, []*materializedRecord{root})
	if _, found := child.root["secret"]; found {
		t.Fatal("nested hidden field remained")
	}
}

func TestHiddenRelationPathsCannotExpandForUsers(t *testing.T) {
	user := Principal{Kind: identity.User, ID: uuid.New()}
	hidden := schema.Field{Name: "owner", Type: schema.TypeRelation, Hidden: true}
	if err := rejectHiddenExpand(user, []expandNode{{name: "owner", field: hidden}}); err == nil {
		t.Fatal("hidden forward expand accepted")
	}
	if err := rejectHiddenExpand(user, []expandNode{{name: "posts_via_owner", kind: expandNodeReverse, field: hidden}}); err == nil {
		t.Fatal("hidden reverse expand accepted")
	}
	if err := rejectHiddenExpand(Principal{Kind: identity.Administrator, ID: uuid.New()}, []expandNode{{name: "owner", field: hidden}}); err != nil {
		t.Fatalf("admin hidden expand: %v", err)
	}
}
