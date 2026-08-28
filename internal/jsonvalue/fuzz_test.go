package jsonvalue

import "testing"

func FuzzStrictPostgreSQLJSON(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"large":9007199254740993,"emoji":"\ud83d\ude00"}`),
		[]byte(`{"nul":"\u0000"}`),
		[]byte(`{"surrogate":"\ud800"}`),
		{'"', 0xff, '"'},
		[]byte(`1e131072`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		valid := Valid(raw)
		canonical, err := Canonical(raw)
		if valid != (err == nil) {
			t.Fatalf("Valid=%t Canonical error=%v", valid, err)
		}
		if err == nil && !Valid(canonical) {
			t.Fatalf("canonical output became invalid: %q", canonical)
		}
	})
}
