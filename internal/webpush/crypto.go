package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Record size for the single aes128gcm record (RFC 8291 §4). The body is
// one record, smaller than this, holding the payload, a 0x02 delimiter, and
// the 16-byte tag.
const recordSize = 4096

// GenerateKeys returns a new VAPID P-256 key pair. public is the
// uncompressed point, private is the 32-byte scalar, both base64url.
func GenerateKeys() (public, private string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	d := make([]byte, 32)
	key.D.FillBytes(d)
	return b64(uncompressed(&key.PublicKey)), b64(d), nil
}

// encrypt builds an aes128gcm body for one subscription (RFC 8188 + RFC 8291).
// uaPublic is the uncompressed p256dh key. auth is the 16-byte auth secret.
func encrypt(plaintext, uaPublic, auth []byte) ([]byte, error) {
	if len(uaPublic) != 65 || len(auth) != 16 {
		return nil, fmt.Errorf("webpush: subscription key length")
	}
	if len(plaintext)+1+16 >= recordSize {
		return nil, fmt.Errorf("webpush: payload too large")
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh: %w", err)
	}
	shared, err := asPriv.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	asPub := asPriv.PublicKey().Bytes()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	cek, nonce, err := derive(shared, auth, salt, uaPublic, asPub)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	padded := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := aead.Seal(nil, nonce, padded, nil)

	out := make([]byte, 0, 21+len(asPub)+len(ciphertext))
	out = append(out, salt...)
	var rs [4]byte
	binary.BigEndian.PutUint32(rs[:], recordSize)
	out = append(out, rs[:]...)
	out = append(out, byte(len(asPub)))
	out = append(out, asPub...)
	out = append(out, ciphertext...)
	return out, nil
}

// derive is RFC 8291 §3.4 followed by the RFC 8188 content key.
// Record sequence 0 leaves the nonce unchanged.
func derive(shared, auth, salt, uaPublic, asPublic []byte) (cek, nonce []byte, err error) {
	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, nil, err
	}
	info := make([]byte, 0, len("WebPush: info")+1+len(uaPublic)+len(asPublic))
	info = append(info, "WebPush: info"...)
	info = append(info, 0)
	info = append(info, uaPublic...)
	info = append(info, asPublic...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	if err != nil {
		return nil, nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, nil, err
	}
	cek, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, nil, err
	}
	nonce, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, nil, err
	}
	return cek, nonce, nil
}

// decrypt reverses encrypt for tests. uaPrivate is the 32-byte scalar.
func decrypt(body, uaPrivate, auth []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, fmt.Errorf("webpush: short body")
	}
	salt := body[:16]
	idlen := int(body[20])
	if len(body) < 21+idlen {
		return nil, fmt.Errorf("webpush: short key id")
	}
	asPub := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]
	uaPriv, err := ecdh.P256().NewPrivateKey(uaPrivate)
	if err != nil {
		return nil, err
	}
	asKey, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		return nil, err
	}
	shared, err := uaPriv.ECDH(asKey)
	if err != nil {
		return nil, err
	}
	cek, nonce, err := derive(shared, auth, salt, uaPriv.PublicKey().Bytes(), asPub)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 || plain[i] != 0x02 {
		return nil, fmt.Errorf("webpush: padding")
	}
	return plain[:i], nil
}

// vapidHeader is the Authorization value for one push request (RFC 8292).
// aud is the origin of the push endpoint. exp is at most 24 hours out.
func vapidHeader(privateB64, publicB64, aud string, exp time.Time) (string, error) {
	priv, err := vapidKey(privateB64)
	if err != nil {
		return "", err
	}
	if exp.After(time.Now().Add(24 * time.Hour)) {
		return "", fmt.Errorf("webpush: vapid exp too far")
	}
	header := b64([]byte(`{"typ":"JWT","alg":"ES256"}`))
	payload := b64([]byte(fmt.Sprintf(`{"aud":%q,"exp":%d,"sub":"mailto:relay@localhost"}`, aud, exp.Unix())))
	signing := header + "." + payload
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	token := signing + "." + b64(sig)
	return "vapid t=" + token + ",k=" + publicB64, nil
}

func vapidKey(privateB64 string) (*ecdsa.PrivateKey, error) {
	d, err := base64.RawURLEncoding.DecodeString(privateB64)
	if err != nil {
		return nil, fmt.Errorf("webpush: vapid private key: %w", err)
	}
	if len(d) != 32 {
		return nil, fmt.Errorf("webpush: vapid private key length")
	}
	priv := new(ecdsa.PrivateKey)
	priv.Curve = elliptic.P256()
	priv.D = new(big.Int).SetBytes(d)
	priv.X, priv.Y = elliptic.P256().ScalarBaseMult(d)
	if priv.X == nil {
		return nil, fmt.Errorf("webpush: vapid private key is invalid")
	}
	return priv, nil
}

func uncompressed(pub *ecdsa.PublicKey) []byte {
	out := make([]byte, 65)
	out[0] = 4
	pub.X.FillBytes(out[1:33])
	pub.Y.FillBytes(out[33:65])
	return out
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeB64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
}
