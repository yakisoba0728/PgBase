package pgbase

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestExtensionAppStoreEnforcesLogicalBudgetAtomically(t *testing.T) {
	control := &extensionAppControl{store: map[string]json.RawMessage{}, storeLimit: 12, storeEntryLimit: defaultExtensionAppStoreEntries}
	set := func(key, value string) error {
		t.Helper()
		_, err := control.callStore("app-control.storeSet", json.RawMessage(`{"key":"`+key+`","value":`+value+`}`))
		return err
	}
	if err := set("a", `"12345"`); err != nil {
		t.Fatal(err)
	}
	if err := set("b", `"12345"`); err == nil || !strings.Contains(err.Error(), "store_limit_exceeded") {
		t.Fatalf("over-budget set error = %v", err)
	}
	if got := string(control.store["a"]); got != `"12345"` {
		t.Fatalf("stored value changed after rejected set: %q", got)
	}
	if err := set("a", `"1"`); err != nil {
		t.Fatalf("smaller replacement: %v", err)
	}
	if err := set("b", `"12345"`); err != nil {
		t.Fatalf("set after replacement freed space: %v", err)
	}
	if _, err := control.callStore("app-control.storeDelete", json.RawMessage(`{"key":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if control.storeBytes != len("b")+len(`"12345"`) {
		t.Fatalf("store bytes = %d", control.storeBytes)
	}
}

func TestJavaScriptOptionsDefaultAndValidateAppStoreBudget(t *testing.T) {
	directory := t.TempDir()
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if options.MaximumAppStoreBytes != defaultExtensionAppStoreBytes {
		t.Fatalf("default store budget = %d", options.MaximumAppStoreBytes)
	}
	if options.MaximumAppStoreEntries != defaultExtensionAppStoreEntries {
		t.Fatalf("default store entry budget = %d", options.MaximumAppStoreEntries)
	}
	if _, err := (JavaScriptOptions{Directory: directory, MaximumAppStoreBytes: 16<<20 + 1}).normalized(); err == nil {
		t.Fatal("oversized app store budget was accepted")
	}
	if _, err := (JavaScriptOptions{Directory: directory, MaximumAppStoreEntries: 65537}).normalized(); err == nil {
		t.Fatal("oversized app store entry budget was accepted")
	}
}

func TestExtensionAppStoreEnforcesEntryBudgetAtomically(t *testing.T) {
	control := &extensionAppControl{store: map[string]json.RawMessage{}, storeLimit: 1024, storeEntryLimit: 1}
	set := func(key string) error {
		_, err := control.callStore("app-control.storeSet", json.RawMessage(`{"key":"`+key+`","value":true}`))
		return err
	}
	if err := set("first"); err != nil {
		t.Fatal(err)
	}
	if err := set("second"); err == nil || !strings.Contains(err.Error(), "store_limit_exceeded") {
		t.Fatalf("entry-cap error = %v", err)
	}
	if err := set("first"); err != nil {
		t.Fatalf("replacement at entry cap: %v", err)
	}
	if _, err := control.callStore("app-control.storeDelete", json.RawMessage(`{"key":"first"}`)); err != nil {
		t.Fatal(err)
	}
	if err := set("second"); err != nil {
		t.Fatalf("entry after delete: %v", err)
	}

	concurrent := &extensionAppControl{store: map[string]json.RawMessage{}, storeLimit: 1024, storeEntryLimit: 1}
	var successful int
	var mu sync.Mutex
	var workers sync.WaitGroup
	for i := range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := concurrent.callStore("app-control.storeSet", json.RawMessage(fmt.Sprintf(`{"key":"key-%d","value":true}`, i)))
			if err == nil {
				mu.Lock()
				successful++
				mu.Unlock()
			}
		}()
	}
	workers.Wait()
	if successful != 1 || len(concurrent.store) != 1 {
		t.Fatalf("concurrent entry limit accepted %d entries; store=%d", successful, len(concurrent.store))
	}
}
