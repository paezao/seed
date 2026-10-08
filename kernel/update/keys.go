package update

import (
	"crypto/ed25519"
	"encoding/hex"
)

// TrustedKeys sign kernel releases. A kernel installs only releases signed by
// one of these; rotating means shipping a release that adds the new key,
// then signing with it.
var TrustedKeys = mustKeys(
	"53b4034b414da1ecccaaef8d30ddded6ebf87bea774f931876dfb70df9393431", // paezao/seed release key (2026-10)
)

func mustKeys(hexKeys ...string) []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, h := range hexKeys {
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != ed25519.PublicKeySize {
			panic("bad trusted key " + h)
		}
		out = append(out, ed25519.PublicKey(b))
	}
	return out
}
