package appupdate

import "testing"

func TestCanonicalGUID(t *testing.T) {
	want := "{3C7E4D7A-80A2-4C79-B7E9-7AA9C4B5B331}"
	for _, input := range []string{want, "3c7e4d7a-80a2-4c79-b7e9-7aa9c4b5b331"} {
		if actual := CanonicalGUID(input); actual != want {
			t.Fatalf("%q: %q", input, actual)
		}
	}
	for _, input := range []string{"", "bad", "{3c7e4d7a-80a2-4c79-b7e9-7aa9c4b5b331", "3c7e4d7a-80a2-4c79-b7e9-7aa9c4b5b33z"} {
		if CanonicalGUID(input) != "" {
			t.Fatal("invalid GUID accepted", input)
		}
	}
}
