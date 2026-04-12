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

func EnsureKeyPair(stateDir string) (*KeyPair, error) {
	privPath := filepath.Join(stateDir, "id_ed25519")
	pubPath := privPath + ".pub"

	if _, err := os.Stat(privPath); err == nil {
		pubBytes, err := os.ReadFile(pubPath)
		if err != nil {
			return nil, fmt.Errorf("reading public key: %w", err)
		}
		return &KeyPair{PrivateKeyPath: privPath, PublicKeyPath: pubPath, PublicKey: string(pubBytes)}, nil
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}

	pemBlock, err := ssh.MarshalPrivateKey(priv, "proxmox-k3s")
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
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

	return &KeyPair{PrivateKeyPath: privPath, PublicKeyPath: pubPath, PublicKey: pubLine}, nil
}
