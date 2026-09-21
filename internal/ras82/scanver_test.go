package ras82

import (
	"bytes"
	"testing"
)

func TestScanVersionEverywhere(t *testing.T) {
	needle := []byte("8.2.19.130")
	tmpls := map[string][]byte{
		"tmplEnv654": tmplEnv654, "tmplNtlm1_474": tmplNtlm1_474,
		"tmplNtlm3_522": tmplNtlm3_522, "tmplNtlm3Domain": tmplNtlm3Domain,
		"tmplSessReq97": tmplSessReq97,
	}
	for n, b := range tmpls {
		c := bytes.Count(b, needle)
		if c > 0 {
			i := bytes.Index(b, needle)
			t.Logf("%s: %d вхождений, контекст: %q", n, c, string(b[max(0, i-25):i+len(needle)+10]))
		} else {
			t.Logf("%s: нет", n)
		}
	}
}
