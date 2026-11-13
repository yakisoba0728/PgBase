package jsvm

import (
	"testing"

	"github.com/dop251/goja"
)

func TestJSONImportPreservesPrototypeNamedProperty(t *testing.T) {
	vm := goja.New()
	const input = `{"__proto__":{"administrator":true},"title":"original"}`
	value, err := decodeValue(vm, []byte(input), 16)
	if err != nil {
		t.Fatal(err)
	}
	object := value.ToObject(vm)
	if inherited := object.Get("administrator"); inherited != nil && !goja.IsUndefined(inherited) {
		t.Fatalf("JSON input changed object prototype: %v", inherited)
	}
	output, err := encodeValue(value, 16)
	if err != nil || string(output) != input {
		t.Fatalf("JSON round trip = %s, %v", output, err)
	}
}

func TestJSONImportPreservesExactMarkerNamedProperty(t *testing.T) {
	vm := goja.New()
	const input = `{"__pgbaseExact":"5","title":"original"}`
	value, err := decodeValue(vm, []byte(input), 16)
	if err != nil {
		t.Fatal(err)
	}
	output, err := encodeValue(value, 16)
	if err != nil || string(output) != input {
		t.Fatalf("JSON round trip = %s, %v", output, err)
	}
}

func TestJSONExportReadsPropertiesOnce(t *testing.T) {
	vm := goja.New()
	value, err := vm.RunString(`let reads = 0; ({get value() { return ++reads; }})`)
	if err != nil {
		t.Fatal(err)
	}
	output, err := encodeValue(value, 16)
	if err != nil || string(output) != `{"value":1}` {
		t.Fatalf("JSON export = %s, %v", output, err)
	}
}

func TestJSONExportChecksDepthBeforeReadingDeepProperties(t *testing.T) {
	vm := goja.New()
	value, err := vm.RunString(`let reads = 0; let value = {get end() { reads++; return true; }}; for (let i = 0; i < 100; i++) value = {nested:value}; value;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeValue(value, 16); err == nil {
		t.Fatal("accepted excessive JSON depth")
	}
	if reads := vm.Get("reads").ToInteger(); reads != 0 {
		t.Fatalf("read beyond JSON depth limit %d times", reads)
	}
}

func TestJSONExportReturnsThrowingGetterError(t *testing.T) {
	vm := goja.New()
	value, err := vm.RunString(`({get value(){throw Error("bad getter")}})`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = encodeValue(value, 16); err == nil {
		t.Fatal("throwing getter accepted")
	}
}

func TestJSONImportEmptyArrayHasZeroLength(t *testing.T) {
	vm := goja.New()
	value, err := decodeValue(vm, []byte(`[]`), 16)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeValue(value, 16)
	if err != nil || string(raw) != "[]" {
		t.Fatalf("empty array=%s err=%v", raw, err)
	}
}
