package crypto

import "crypto/sha256"

// sha256New is a small adapter for hkdf.New.
var sha256New = sha256.New
