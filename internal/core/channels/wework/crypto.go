package wework

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"strings"
)

const weworkPKCS7BlockSize = 32

func decodeWeWorkAESKey(encoded string) ([]byte, error) {
	key := strings.TrimSpace(encoded)
	if key == "" {
		return nil, fmt.Errorf("empty aes key")
	}

	candidates := []string{key}
	if !strings.HasSuffix(key, "=") {
		candidates = append(candidates, key+"=")
	}

	for _, candidate := range candidates {
		if decoded, err := base64.StdEncoding.DecodeString(candidate); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
		if decoded, err := base64.RawStdEncoding.DecodeString(candidate); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}

	if len(key) == 32 {
		return []byte(key), nil
	}

	return nil, fmt.Errorf("invalid aes key length")
}

func decryptWeWorkCBCPayload(ciphertext []byte, encodedKey string) ([]byte, error) {
	key, err := decodeWeWorkAESKey(encodedKey)
	if err != nil {
		return nil, err
	}
	return decryptWeWorkCBCPayloadWithKey(ciphertext, key)
}

func decryptWeWorkCBCPayloadWithKey(ciphertext, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid aes key length: %d, expected 32", len(key))
	}
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("empty ciphertext")
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid ciphertext length: %d", len(ciphertext))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher failed: %w", err)
	}

	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plaintext, ciphertext)

	return unpadWeWorkPKCS7(plaintext)
}

func unpadWeWorkPKCS7(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("empty plaintext after decryption")
	}

	padding := int(plaintext[len(plaintext)-1])
	if padding < 1 || padding > weworkPKCS7BlockSize {
		return nil, fmt.Errorf("invalid pkcs7 padding value: %d (block size %d)", padding, weworkPKCS7BlockSize)
	}
	if padding > len(plaintext) {
		return nil, fmt.Errorf("pkcs7 padding %d exceeds plaintext length %d", padding, len(plaintext))
	}

	for _, b := range plaintext[len(plaintext)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("invalid pkcs7 padding content")
		}
	}

	return plaintext[:len(plaintext)-padding], nil
}
