package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// SigningKeyManager is responsible for providing signing and verification keys.
// Implementations can load keys from environment variables, files, or external KMS.
type SigningKeyManager interface {
	// SigningKey returns the private key used for signing tokens.
	SigningKey(ctx context.Context) (crypto.PrivateKey, error)

	// VerificationKey returns the public key used to verify token signatures.
	VerificationKey(ctx context.Context) (crypto.PublicKey, error)

	// KeyID returns the identifier for the current signing key (used in JWT kid header).
	KeyID(ctx context.Context) (string, error)
}

// envKeyManager loads signing keys from an environment variable.
type envKeyManager struct {
	keyID  string
	envVar string
}

// NewEnvKeyManager creates a SigningKeyManager that reads a PEM-encoded
// private key from the given environment variable.
func NewEnvKeyManager(keyID, envVar string) SigningKeyManager {
	return &envKeyManager{keyID: keyID, envVar: envVar}
}

func (m *envKeyManager) SigningKey(ctx context.Context) (crypto.PrivateKey, error) {
	raw := os.Getenv(m.envVar)
	if raw == "" {
		return nil, fmt.Errorf("signing key: environment variable %s is not set", m.envVar)
	}
	return parsePrivateKey([]byte(raw))
}

func (m *envKeyManager) VerificationKey(ctx context.Context) (crypto.PublicKey, error) {
	priv, err := m.SigningKey(ctx)
	if err != nil {
		return nil, err
	}
	signer, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("signing key is not an RSA private key")
	}
	return &signer.PublicKey, nil
}

func (m *envKeyManager) KeyID(ctx context.Context) (string, error) {
	return m.keyID, nil
}

// fileKeyManager loads signing keys from a PEM file on disk.
type fileKeyManager struct {
	keyID    string
	filePath string
	cached   crypto.PrivateKey
}

// NewFileKeyManager creates a SigningKeyManager that reads a PEM-encoded
// private key from the given file path. The key is cached in memory after
// the first read.
func NewFileKeyManager(keyID, filePath string) SigningKeyManager {
	return &fileKeyManager{keyID: keyID, filePath: filePath}
}

func (m *fileKeyManager) SigningKey(ctx context.Context) (crypto.PrivateKey, error) {
	if m.cached != nil {
		return m.cached, nil
	}
	raw, err := os.ReadFile(m.filePath)
	if err != nil {
		return nil, fmt.Errorf("read signing key file: %w", err)
	}
	priv, err := parsePrivateKey(raw)
	if err != nil {
		return nil, err
	}
	m.cached = priv
	return priv, nil
}

func (m *fileKeyManager) VerificationKey(ctx context.Context) (crypto.PublicKey, error) {
	priv, err := m.SigningKey(ctx)
	if err != nil {
		return nil, err
	}
	signer, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("signing key is not an RSA private key")
	}
	return &signer.PublicKey, nil
}

func (m *fileKeyManager) KeyID(ctx context.Context) (string, error) {
	return m.keyID, nil
}

// NewSigningKeyManager creates the appropriate SigningKeyManager based on config.
func NewSigningKeyManager(cfg SigningKeyConfig) (SigningKeyManager, error) {
	keyID := cfg.KeyID
	if keyID == "" {
		keyID = DefaultSigningKeyID
	}
	switch cfg.Source {
	case "env":
		envVar := cfg.EnvVarName
		if envVar == "" {
			envVar = DefaultSigningKeyEnvVar
		}
		return NewEnvKeyManager(keyID, envVar), nil
	case "file":
		if cfg.FilePath == "" {
			return nil, errors.New("file_path is required when source is 'file'")
		}
		return NewFileKeyManager(keyID, cfg.FilePath), nil
	default:
		return nil, fmt.Errorf("unsupported signing key source: %s", cfg.Source)
	}
}

// GenerateKeyPair generates a new RSA key pair for testing.
func GenerateKeyPair() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

// parsePrivateKey decodes a PEM-encoded RSA private key.
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found in key data")
	}

	// Try PKCS1 first, then PKCS8
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("PEM key is not an RSA private key")
		}
		return rsaKey, nil
	}
	return nil, errors.New("failed to parse PEM private key: not PKCS1 or PKCS8 format")
}
