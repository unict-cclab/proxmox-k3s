package util

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

type KeyPair struct {
	PrivateKeyPath string
	PublicKeyPath  string
	PublicKey      string
}

// EnsureKeyPair ensures a key pair exists under stateDir/id_ed25519.
// It is a convenience wrapper around EnsureKeyPairAt.
func EnsureKeyPair(stateDir string) (*KeyPair, error) {
	return EnsureKeyPairAt(filepath.Join(stateDir, "id_ed25519"))
}

// EnsureKeyPairAt ensures a key pair exists at the given private key path.
// If the key already exists it is read and returned; if it does not exist a new
// ed25519 key pair is generated and written there. The public key is stored at
// keyPath+".pub". If keyPath already exists but the .pub file is missing, the
// public key is derived from the private key.
func EnsureKeyPairAt(keyPath string) (*KeyPair, error) {
	pubPath := keyPath + ".pub"

	if _, err := os.Stat(keyPath); err == nil {
		// Key file exists — read the public key.
		if pubBytes, err := os.ReadFile(pubPath); err == nil {
			return &KeyPair{PrivateKeyPath: keyPath, PublicKeyPath: pubPath, PublicKey: string(pubBytes)}, nil
		}
		// .pub file missing — derive public key from the private key.
		signer, err := signerFromFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("parsing private key %s: %w", keyPath, err)
		}
		pubLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
		if err := os.WriteFile(pubPath, []byte(pubLine), 0644); err != nil {
			return nil, fmt.Errorf("writing public key: %w", err)
		}
		return &KeyPair{PrivateKeyPath: keyPath, PublicKeyPath: pubPath, PublicKey: pubLine}, nil
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, fmt.Errorf("creating key directory: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}

	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		return nil, fmt.Errorf("writing private key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("encoding public key: %w", err)
	}
	pubLine := string(ssh.MarshalAuthorizedKey(sshPub))
	if err := os.WriteFile(pubPath, []byte(pubLine), 0644); err != nil {
		return nil, fmt.Errorf("writing public key: %w", err)
	}

	return &KeyPair{PrivateKeyPath: keyPath, PublicKeyPath: pubPath, PublicKey: pubLine}, nil
}
