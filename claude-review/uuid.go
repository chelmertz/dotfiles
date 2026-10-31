package main

import (
	"crypto/sha1" //nolint:gosec // RFC 4122 mandates SHA-1 for version 5 UUIDs; not used for security.
	"encoding/hex"
	"fmt"
	"strings"
)

// uuidV5 derives a name-based UUID (RFC 4122 section 4.3) from a namespace
// UUID and a name. The standard library has no UUID package, and this is the
// only UUID operation the tool needs.
func uuidV5(namespace, name string) string {
	ns, err := hex.DecodeString(strings.ReplaceAll(namespace, "-", ""))
	if err != nil || len(ns) != 16 {
		panic(fmt.Sprintf("uuidV5: bad namespace %q", namespace))
	}
	h := sha1.New() //nolint:gosec // see import comment
	h.Write(ns)
	h.Write([]byte(name))
	sum := h.Sum(nil)[:16]
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	s := hex.EncodeToString(sum)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
