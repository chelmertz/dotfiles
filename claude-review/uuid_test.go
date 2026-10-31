package main

import "testing"

// RFC 4122 appendix vector plus the python.org example every library uses.
func TestUUIDv5_KnownVectors(t *testing.T) {
	ns := "6ba7b810-9dad-11d1-80b4-00c04fd430c8" // DNS namespace
	if got := uuidV5(ns, "python.org"); got != "886313e1-3b8a-5372-9b90-0c9aee199e5d" {
		t.Errorf("got %s", got)
	}
	if got := uuidV5(ns, "www.example.com"); got != "2ed6657d-e927-568b-95e1-2665a8aea6a2" {
		t.Errorf("got %s", got)
	}
}

func TestUUIDv5_SetsVersionAndVariantBits(t *testing.T) {
	got := uuidV5("6ba7b811-9dad-11d1-80b4-00c04fd430c8", "anything")
	if got[14] != '5' {
		t.Errorf("version nibble: %s", got)
	}
	switch got[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Errorf("variant nibble: %s", got)
	}
}

func TestUUIDv5_BadNamespacePanics(t *testing.T) {
	for _, ns := range []string{badTarget, "6ba7b811-9dad-11d1-80b4-00c04fd430"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%q: expected panic", ns)
				}
			}()
			uuidV5(ns, "x")
		}()
	}
}
