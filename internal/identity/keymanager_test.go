package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func pemEncodePrivateKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func pemEncodePrivateKeyPKCS8(key *rsa.PrivateKey) []byte {
	bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: bytes,
	})
}

func TestGenerateKeyPair(t *testing.T) {
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if key.N.BitLen() != 2048 {
		t.Fatalf("expected 2048-bit key, got %d bits", key.N.BitLen())
	}
	// Verify it can sign
	hash := sha256.Sum256([]byte("test"))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestParsePrivateKey_PKCS1(t *testing.T) {
	key, _ := GenerateKeyPair()
	pemData := pemEncodePrivateKey(key)
	parsed, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey: %v", err)
	}
	if parsed.N.Cmp(key.N) != 0 {
		t.Fatal("parsed key does not match original")
	}
}

func TestParsePrivateKey_PKCS8(t *testing.T) {
	key, _ := GenerateKeyPair()
	pemData := pemEncodePrivateKeyPKCS8(key)
	parsed, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey PKCS8: %v", err)
	}
	if parsed.N.Cmp(key.N) != 0 {
		t.Fatal("parsed key does not match original")
	}
}

func TestParsePrivateKey_InvalidPEM(t *testing.T) {
	_, err := parsePrivateKey([]byte("not-a-pem-block"))
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestParsePrivateKey_Empty(t *testing.T) {
	_, err := parsePrivateKey([]byte{})
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestEnvKeyManager_SigningKey(t *testing.T) {
	key, _ := GenerateKeyPair()
	pemData := pemEncodePrivateKey(key)

	_ = os.Setenv("FOREMAN_TEST_SIGNING_KEY", string(pemData))
	t.Cleanup(func() { _ = os.Unsetenv("FOREMAN_TEST_SIGNING_KEY") })

	mgr := NewEnvKeyManager("test-key-1", "FOREMAN_TEST_SIGNING_KEY")
	ctx := context.Background()

	priv, err := mgr.SigningKey(ctx)
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	rsaPriv, ok := priv.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected *rsa.PrivateKey")
	}
	if rsaPriv.N.Cmp(key.N) != 0 {
		t.Fatal("key mismatch")
	}
}

func TestEnvKeyManager_VerificationKey(t *testing.T) {
	key, _ := GenerateKeyPair()
	pemData := pemEncodePrivateKey(key)

	_ = os.Setenv("FOREMAN_TEST_SIGNING_KEY_V", string(pemData))
	t.Cleanup(func() { _ = os.Unsetenv("FOREMAN_TEST_SIGNING_KEY_V") })

	mgr := NewEnvKeyManager("test-key-1", "FOREMAN_TEST_SIGNING_KEY_V")
	ctx := context.Background()

	pub, err := mgr.VerificationKey(ctx)
	if err != nil {
		t.Fatalf("VerificationKey: %v", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		t.Fatal("expected *rsa.PublicKey")
	}
	if rsaPub.N.Cmp(key.N) != 0 {
		t.Fatal("public key mismatch")
	}
}

func TestEnvKeyManager_KeyID(t *testing.T) {
	mgr := NewEnvKeyManager("env-key-1", "FOREMAN_TEST_NONEXISTENT")
	ctx := context.Background()

	kid, err := mgr.KeyID(ctx)
	if err != nil {
		t.Fatalf("KeyID: %v", err)
	}
	if kid != "env-key-1" {
		t.Fatalf("expected 'env-key-1', got %q", kid)
	}
}

func TestEnvKeyManager_MissingEnvVar(t *testing.T) {
	mgr := NewEnvKeyManager("test", "FOREMAN_TEST_NONEXISTENT_KEY")
	ctx := context.Background()

	_, err := mgr.SigningKey(ctx)
	if err == nil {
		t.Fatal("expected error for missing environment variable")
	}
}

func TestFileKeyManager_SigningKey(t *testing.T) {
	key, _ := GenerateKeyPair()
	pemData := pemEncodePrivateKey(key)

	tmp, err := os.CreateTemp("", "test-key.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp.Name()) })
	if _, err := tmp.Write(pemData); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = tmp.Close()

	mgr := NewFileKeyManager("file-key-1", tmp.Name())
	ctx := context.Background()

	priv, err := mgr.SigningKey(ctx)
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	rsaPriv, ok := priv.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected *rsa.PrivateKey")
	}
	if rsaPriv.N.Cmp(key.N) != 0 {
		t.Fatal("key mismatch")
	}

	// Second call should use cache (no error)
	priv2, err := mgr.SigningKey(ctx)
	if err != nil {
		t.Fatalf("SigningKey (cached): %v", err)
	}
	if priv2 != priv {
		t.Fatal("expected cached key to be same instance")
	}
}

func TestFileKeyManager_VerificationKey(t *testing.T) {
	key, _ := GenerateKeyPair()
	pemData := pemEncodePrivateKey(key)

	tmp, err := os.CreateTemp("", "test-key-verify.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(pemData); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = tmp.Close()

	mgr := NewFileKeyManager("file-key-v", tmp.Name())
	ctx := context.Background()

	pub, err := mgr.VerificationKey(ctx)
	if err != nil {
		t.Fatalf("VerificationKey: %v", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		t.Fatal("expected *rsa.PublicKey")
	}
	if rsaPub.N.Cmp(key.N) != 0 {
		t.Fatal("public key mismatch")
	}
}

func TestFileKeyManager_MissingFile(t *testing.T) {
	mgr := NewFileKeyManager("test", "/tmp/nonexistent-test-key.pem")
	ctx := context.Background()

	_, err := mgr.SigningKey(ctx)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFileKeyManager_InvalidPEM(t *testing.T) {
	tmp, err := os.CreateTemp("", "invalid-key.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp.Name()) })
	if _, err := tmp.Write([]byte("not a valid pem")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = tmp.Close()

	mgr := NewFileKeyManager("test", tmp.Name())
	ctx := context.Background()

	_, err = mgr.SigningKey(ctx)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestNewSigningKeyManager_EnvDefault(t *testing.T) {
	key, _ := GenerateKeyPair()
	_ = os.Setenv(DefaultSigningKeyEnvVar, string(pemEncodePrivateKey(key)))
	t.Cleanup(func() { _ = os.Unsetenv(DefaultSigningKeyEnvVar) })

	mgr, err := NewSigningKeyManager(SigningKeyConfig{Source: "env"})
	if err != nil {
		t.Fatalf("NewSigningKeyManager: %v", err)
	}
	ctx := context.Background()

	kid, _ := mgr.KeyID(ctx)
	if kid != DefaultSigningKeyID {
		t.Fatalf("expected default key ID %q, got %q", DefaultSigningKeyID, kid)
	}
	priv, err := mgr.SigningKey(ctx)
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	if priv == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestNewSigningKeyManager_File(t *testing.T) {
	key, _ := GenerateKeyPair()
	tmp, err := os.CreateTemp("", "test-key.pem")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp.Name()) })
	if _, err := tmp.Write(pemEncodePrivateKey(key)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = tmp.Close()

	mgr, err := NewSigningKeyManager(SigningKeyConfig{
		Source:   "file",
		KeyID:    "my-file-key",
		FilePath: tmp.Name(),
	})
	if err != nil {
		t.Fatalf("NewSigningKeyManager: %v", err)
	}
	ctx := context.Background()

	kid, _ := mgr.KeyID(ctx)
	if kid != "my-file-key" {
		t.Fatalf("expected 'my-file-key', got %q", kid)
	}
	priv, err := mgr.SigningKey(ctx)
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	if priv == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestNewSigningKeyManager_InvalidSource(t *testing.T) {
	_, err := NewSigningKeyManager(SigningKeyConfig{Source: "hsm"})
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	key, _ := GenerateKeyPair()
	ctx := context.Background()

	// Test with env manager
	_ = os.Setenv("FOREMAN_TEST_RT_KEY", string(pemEncodePrivateKey(key)))
	t.Cleanup(func() { _ = os.Unsetenv("FOREMAN_TEST_RT_KEY") })

	mgr := NewEnvKeyManager("rt-key", "FOREMAN_TEST_RT_KEY")
	priv, err := mgr.SigningKey(ctx)
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	pub, err := mgr.VerificationKey(ctx)
	if err != nil {
		t.Fatalf("VerificationKey: %v", err)
	}

	rsaPriv := priv.(*rsa.PrivateKey)
	rsaPub := pub.(*rsa.PublicKey)

	// Sign with private key
	msg := []byte("hello world")
	hash := sha256.Sum256(msg)
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaPriv, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Verify with public key
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hash[:], sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
