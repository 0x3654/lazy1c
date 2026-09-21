package mmc83

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// nameUUID — детерминированный uuid от имени (SHA-1, v5-подобная разметка),
// чтобы связывать базы/сеансы без настоящих uuid баз (как ras82.NameUUID).
func nameUUID(name string) string {
	h := sha1.Sum([]byte("lazy1c-83:" + name))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}
