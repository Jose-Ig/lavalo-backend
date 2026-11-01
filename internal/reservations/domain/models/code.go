package models

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
)

var codeAlphabet = base32.NewEncoding("23456789ABCDEFGHJKLMNPQRSTUVWXYZ").WithPadding(base32.NoPadding)

// ConfirmationCode derives the stable customer code without storing it in plaintext.
func (r Reservation) ConfirmationCode() string {
	if len(r.AccessTokenHash) != 64 || r.ID == 0 {
		return ""
	}
	if r.CodeVersion == 2 {
		digest := sha256.Sum256([]byte(fmt.Sprintf("uash:confirmation:v2:%d:%s:%d", r.ID, r.AccessTokenHash, r.CodeNonce)))
		return fmt.Sprintf("%06d", binary.BigEndian.Uint64(digest[:8])%1000000)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("uash:confirmation:v1:%d:%s", r.ID, r.AccessTokenHash)))
	raw := codeAlphabet.EncodeToString(digest[:10]) // 80 bits, 16 printable characters.
	return "UASH-" + raw[:4] + "-" + raw[4:8] + "-" + raw[8:12] + "-" + raw[12:]
}
