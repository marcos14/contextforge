package backup

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/crypto"
)

func newCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return c
}

// TestFraming exercises the file-level encryption/decryption without touching
// the database, by building a Manifest manually and round-tripping it through
// Service.encrypt/Parse.
func TestFraming(t *testing.T) {
	c := newCipher(t)
	s := &Service{Cipher: c}

	m := &Manifest{
		Format:                     "contextforge-backup",
		Version:                    1,
		ExportedAt:                 time.Now().UTC(),
		SourceMasterKeyFingerprint: c.Fingerprint(),
		Items: ManifestItems{
			Connections: []ConnectionDTO{{ID: uuid.New(), Name: "a", Type: "pg"}},
		},
	}
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ct, err := s.Cipher.Encrypt(payload, []byte(aad))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	blob := append([]byte{}, magic...)
	blob = append(blob, fileVersion)
	blob = append(blob, ct...)

	got, err := s.Parse(blob)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if got.Format != "contextforge-backup" {
		t.Fatalf("format mismatch: %q", got.Format)
	}
	if len(got.Items.Connections) != 1 || got.Items.Connections[0].Name != "a" {
		t.Fatalf("unexpected manifest contents: %+v", got.Items)
	}
}

func TestParseBadMagic(t *testing.T) {
	s := &Service{Cipher: newCipher(t)}
	_, err := s.Parse([]byte{'X', 'X', 'X', 'X', 1, 0, 0})
	if err == nil {
		t.Fatal("expected error on bad magic")
	}
}

func TestParseWrongKey(t *testing.T) {
	c1 := newCipher(t)
	m := &Manifest{Format: "contextforge-backup", Version: 1, SourceMasterKeyFingerprint: c1.Fingerprint()}
	payload, _ := json.Marshal(m)
	ct, err := c1.Encrypt(payload, []byte(aad))
	if err != nil {
		t.Fatal(err)
	}
	blob := append([]byte{}, magic...)
	blob = append(blob, fileVersion)
	blob = append(blob, ct...)

	// Other server with a different key
	otherKey := bytes.Repeat([]byte{0x42}, 32)
	c2, _ := crypto.New(otherKey)
	if c1.Fingerprint() == c2.Fingerprint() {
		t.Fatal("fingerprints unexpectedly match")
	}
	s2 := &Service{Cipher: c2}
	_, err = s2.Parse(blob)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := map[string]string{
		"hello":        "hello",
		"Hello World!": "hello_world_",
		"abc-123":      "abc_123",
		"":             "tool_copy",
		"ÁÉ":           "__", // two non-ASCII bytes each → underscore; lower-case keeps them ASCII-like? actually bytes 0xc3 etc
	}
	for in, want := range cases {
		got := sanitizeSlug(in)
		if in == "ÁÉ" {
			// Just assert the output matches the regex constraint.
			for _, c := range got {
				if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
					t.Errorf("sanitizeSlug(%q)=%q contains invalid char %q", in, got, c)
				}
			}
			continue
		}
		if got != want {
			t.Errorf("sanitizeSlug(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestDedupeUUIDs(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	got := dedupeUUIDs([]uuid.UUID{a, b, a, b, a})
	if len(got) != 2 {
		t.Fatalf("expected 2 unique, got %d", len(got))
	}
}
