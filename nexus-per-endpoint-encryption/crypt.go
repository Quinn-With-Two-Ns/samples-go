package nexusperendpointencryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// keyA and keyB are mock 32-byte AES keys keyed by keyID. In production these
// values would be fetched from a KMS, never hardcoded.
var (
	keyA = []byte("key-a-test-key-test-key-test!!aa")
	keyB = []byte("key-b-test-key-test-key-test!!bb")
)

// getKey returns the AES key bytes for the given keyID, or an error if the
// keyID is unknown. The codec server uses the same getKey to decode payloads
// produced under either key.
func getKey(keyID string) ([]byte, error) {
	switch keyID {
	case "key-a":
		return keyA, nil
	case "key-b":
		return keyB, nil
	default:
		return nil, fmt.Errorf("unknown keyID: %s", keyID)
	}
}

func encrypt(plainData []byte, key []byte) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plainData, nil), nil
}

func decrypt(encryptedData []byte, key []byte) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(encryptedData) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: length %d", len(encryptedData))
	}

	nonce, encryptedData := encryptedData[:nonceSize], encryptedData[nonceSize:]
	return gcm.Open(nil, nonce, encryptedData, nil)
}
