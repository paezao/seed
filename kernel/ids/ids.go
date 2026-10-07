// Package ids generates sortable, prefixed identifiers (e.g. evo_01J...).
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32

// New returns prefix + "_" + a 26-char ULID-style identifier: 48 bits of
// millisecond timestamp followed by 80 random bits. IDs sort by creation time.
func New(prefix string) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixMilli())<<16)
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err)
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteByte('_')
	// encode 128 bits as 26 base32 chars (130 bits, top 2 zero)
	var acc uint64
	hi := binary.BigEndian.Uint64(b[0:8])
	lo := binary.BigEndian.Uint64(b[8:16])
	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		acc = lo & 31
		lo = (lo >> 5) | (hi << 59)
		hi >>= 5
		out[i] = alphabet[acc]
	}
	sb.Write(out)
	return sb.String()
}

// Short returns the last n characters of an id, handy for names with length limits.
func Short(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[len(id)-n:]
}
