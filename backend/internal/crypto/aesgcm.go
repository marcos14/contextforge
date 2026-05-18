// Package crypto provides authenticated encryption for secrets at rest.
//
// Each plaintext is encrypted with a fresh Data Encryption Key (DEK), which is
// itself derived from the configured Master Key via HKDF using a random salt.
// The resulting blob carries the salt and nonce, so rotating the master key
// only requires re-deriving DEKs (no plaintext is needed for migration apart
// from the standard decrypt+encrypt cycle).
//
// Blob layout (binary, all big-endian):
//
//	[1 byte version=1][16 bytes salt][12 bytes nonce][N bytes ciphertext+tag]
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	version    byte = 1
	saltSize        = 16
	nonceSize       = 12
	dekSize         = 32
	headerSize      = 1 + saltSize + nonceSize
)

// Cipher encrypts and decrypts secrets using AES-256-GCM with per-record DEKs.
type Cipher struct {
	masterKey []byte
}

// New creates a Cipher with the given 32-byte master key.
func New(masterKey []byte) (*Cipher, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}
	return &Cipher{masterKey: append([]byte(nil), masterKey...)}, nil
}

// Encrypt encrypts plaintext, returning a self-describing blob.
// The optional aad (additional authenticated data) is bound to the ciphertext
// but not stored. Callers must pass the same aad on Decrypt.
func (c *Cipher) Encrypt(plaintext, aad []byte) ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	dek, err := c.deriveDEK(salt)
	if err != nil {
		return nil, err
	}
	defer zero(dek)

	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, headerSize+len(plaintext)+gcm.Overhead())
	out = append(out, version)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, aad)
	return out, nil
}

// Decrypt reverses Encrypt.
func (c *Cipher) Decrypt(blob, aad []byte) ([]byte, error) {
	if len(blob) < headerSize+16 {
		return nil, errors.New("ciphertext too short")
	}
	if blob[0] != version {
		return nil, fmt.Errorf("unsupported ciphertext version %d", blob[0])
	}
	salt := blob[1 : 1+saltSize]
	nonce := blob[1+saltSize : headerSize]
	ct := blob[headerSize:]

	dek, err := c.deriveDEK(salt)
	if err != nil {
		return nil, err
	}
	defer zero(dek)

	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, aad)
}

func (c *Cipher) deriveDEK(salt []byte) ([]byte, error) {
	h := hkdf.New(sha256New, c.masterKey, salt, []byte("contextforge/dek/v1"))
	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(h, dek); err != nil {
		return nil, err
	}
	return dek, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Fingerprint returns a short, non-secret identifier derived from the master
// key. It is the first 8 bytes of SHA-256(masterKey) hex-encoded. Useful to
// detect at-rest mismatches (e.g. a backup encrypted with a different master
// key) without exposing the key material itself.
func (c *Cipher) Fingerprint() string {
	sum := sha256.Sum256(c.masterKey)
	return hex.EncodeToString(sum[:8])
}
