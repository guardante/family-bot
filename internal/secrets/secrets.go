// Package secrets: шифрование секретов (токены, пароли приложений) перед
// записью в базу. AES-256-GCM. Мастер-ключ — из ENCRYPTION_KEY, а если та
// не задана, выводится из BOT_TOKEN.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

const (
	prefix   = "enc:v1:" // пометка зашифрованных значений
	keyInfo  = "nadia-bot/secret-box"
	nonceLen = 12
)

type Cipher struct{ key []byte }

func New(masterSecret string) *Cipher {
	sum := sha256.Sum256([]byte(keyInfo + ":" + masterSecret))
	return &Cipher{key: sum[:]}
}

func (c *Cipher) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt шифрует строку. Пустые строки и уже зашифрованные возвращает как есть.
func (c *Cipher) Encrypt(plain string) string {
	if plain == "" || strings.HasPrefix(plain, prefix) {
		return plain
	}
	gcm, err := c.gcm()
	if err != nil {
		return plain
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return plain
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed)
}

// Decrypt расшифровывает. Значение без префикса — легаси-открытый текст, вернём как есть.
func (c *Cipher) Decrypt(value string) string {
	if !strings.HasPrefix(value, prefix) {
		return value
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return ""
	}
	gcm, err := c.gcm()
	if err != nil {
		return ""
	}
	if len(raw) < gcm.NonceSize() {
		return ""
	}
	nonce, sealed := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}
