// Package auth provides admin-UI authentication (JWT) and MCP client token
// hashing/verification.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ============== Admin UI (bcrypt + JWT) ==============

const (
	defaultAccessTTL  = 15 * time.Minute
	defaultRefreshTTL = 30 * 24 * time.Hour
)

// HashPassword hashes a plain password with bcrypt cost 12.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword returns nil if the password matches the hash.
func VerifyPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

// Claims is the JWT payload for the admin UI.
type Claims struct {
	UserID uuid.UUID `json:"uid"`
	Role   string    `json:"role"`
	Type   string    `json:"typ"` // "access" or "refresh"
	jwt.RegisteredClaims
}

// Signer issues and validates JWTs.
type Signer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewSigner builds a Signer. A non-positive accessTTL/refreshTTL falls back to
// the package defaults (15 min / 30 days).
func NewSigner(secret []byte, accessTTL, refreshTTL time.Duration) *Signer {
	if accessTTL <= 0 {
		accessTTL = defaultAccessTTL
	}
	if refreshTTL <= 0 {
		refreshTTL = defaultRefreshTTL
	}
	return &Signer{secret: secret, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

func (s *Signer) issue(userID uuid.UUID, role, typ string, ttl time.Duration) (string, error) {
	c := Claims{
		UserID: userID,
		Role:   role,
		Type:   typ,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			Issuer:    "contextforge",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString(s.secret)
}

// IssueAccess returns a short-lived access token.
func (s *Signer) IssueAccess(userID uuid.UUID, role string) (string, error) {
	return s.issue(userID, role, "access", s.accessTTL)
}

// IssueRefresh returns a longer-lived refresh token.
func (s *Signer) IssueRefresh(userID uuid.UUID, role string) (string, error) {
	return s.issue(userID, role, "refresh", s.refreshTTL)
}

// Verify parses and validates a JWT, returning the claims.
func (s *Signer) Verify(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}

// ============== MCP client tokens ==============
//
// Format: "mcb_<prefix>_<secret>". The prefix is stored in cleartext for
// fast lookup; the secret is stored as sha256 (no plaintext ever persisted).

const (
	tokenScheme = "mcb"
	prefixLen   = 8
	secretLen   = 32 // hex-encoded length; raw 16 bytes
)

// GenerateClientToken returns a freshly generated token string and its
// (prefix, hashedSecret) pair for storage. The plaintext token must be shown
// to the user only once.
func GenerateClientToken() (token, prefix string, hashedSecret []byte, err error) {
	pb := make([]byte, 4) // -> 8 hex chars
	if _, err = rand.Read(pb); err != nil {
		return "", "", nil, err
	}
	sb := make([]byte, 16) // -> 32 hex chars
	if _, err = rand.Read(sb); err != nil {
		return "", "", nil, err
	}
	prefix = hex.EncodeToString(pb)
	secret := hex.EncodeToString(sb)
	sum := sha256.Sum256([]byte(secret))
	token = fmt.Sprintf("%s_%s_%s", tokenScheme, prefix, secret)
	return token, prefix, sum[:], nil
}

// ParseClientToken extracts (prefix, secret) from a raw token string.
func ParseClientToken(raw string) (prefix, secret string, ok bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "Bearer ")
	raw = strings.TrimPrefix(raw, "bearer ")
	parts := strings.Split(raw, "_")
	if len(parts) != 3 || parts[0] != tokenScheme {
		return "", "", false
	}
	if len(parts[1]) != prefixLen || len(parts[2]) != secretLen {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// VerifyClientSecret returns true if the provided secret hashes to the stored hash.
func VerifyClientSecret(secret string, hashed []byte) bool {
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], hashed) == 1
}
