package realtime

import "testing"

func TestReferencesACLRecordIsConservative(t *testing.T) {
	for sql, want := range map[string]bool{
		"true":                         false,
		"false":                        false,
		"event.owner_user_id=$3":       false,
		`"event"."owner_user_id" = $3`: false,
		`"acl_record"."title" = $3`:    true,
		`acl_record.title = $3`:        true,
		`(true) AND (EXISTS (SELECT 1 FROM app."tags" t WHERE t.id = ANY("acl_record"."tags")))`: true,
		// A longer alias that merely contains the name still keeps the join.
		`"acl_record_target"."id" = $4`: true,
	} {
		if got := referencesACLRecord(sql); got != want {
			t.Errorf("referencesACLRecord(%q) = %t, want %t", sql, got, want)
		}
	}
}
