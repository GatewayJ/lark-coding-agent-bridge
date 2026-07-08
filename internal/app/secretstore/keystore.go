package secretstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
	"golang.org/x/crypto/pbkdf2"
)

const (
	keyLen     = 32
	ivLen      = 12
	tagLen     = 16
	pbkdf2Iter = 100_000
	fileVer    = 1
)

type KeystorePaths struct {
	SecretsFile         string
	KeystoreSaltFile    string
	SecretsGetterScript string
}

type KeystoreOptions struct {
	Paths KeystorePaths
	Seed  string
	Rand  io.Reader
}

type Keystore struct {
	paths KeystorePaths
	seed  string
	rand  io.Reader
}

type envelope struct {
	IV   string `json:"iv"`
	Data string `json:"data"`
	Tag  string `json:"tag"`
}

type storeFile struct {
	Version int                 `json:"version"`
	Entries map[string]envelope `json:"entries"`
}

func DefaultKeystorePaths() (KeystorePaths, error) {
	paths, err := apppaths.Resolve(apppaths.Options{})
	if err != nil {
		return KeystorePaths{}, err
	}
	return KeystorePaths{
		SecretsFile:         paths.SecretsFile,
		KeystoreSaltFile:    paths.KeystoreSaltFile,
		SecretsGetterScript: paths.SecretsGetterScript,
	}, nil
}

func NewKeystore(options KeystoreOptions) (*Keystore, error) {
	paths := options.Paths
	if paths.SecretsFile == "" || paths.KeystoreSaltFile == "" {
		defaults, err := DefaultKeystorePaths()
		if err != nil {
			return nil, err
		}
		if paths.SecretsFile == "" {
			paths.SecretsFile = defaults.SecretsFile
		}
		if paths.KeystoreSaltFile == "" {
			paths.KeystoreSaltFile = defaults.KeystoreSaltFile
		}
		if paths.SecretsGetterScript == "" {
			paths.SecretsGetterScript = defaults.SecretsGetterScript
		}
	}
	seed := options.Seed
	if seed == "" {
		var err error
		seed, err = defaultSeed()
		if err != nil {
			return nil, err
		}
	}
	random := options.Rand
	if random == nil {
		random = rand.Reader
	}
	return &Keystore{paths: paths, seed: seed, rand: random}, nil
}

func (k *Keystore) Paths() KeystorePaths {
	return k.paths
}

func (k *Keystore) GetSecret(id string) (string, bool, error) {
	store, err := k.readStore()
	if err != nil {
		return "", false, err
	}
	env, ok := store.Entries[id]
	if !ok {
		return "", false, nil
	}
	key, err := k.deriveKey()
	if err != nil {
		return "", false, err
	}
	value, err := decryptEnvelope(key, env)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (k *Keystore) SetSecret(id string, plaintext string) error {
	key, err := k.deriveKey()
	if err != nil {
		return err
	}
	env, err := k.encrypt(key, plaintext)
	if err != nil {
		return err
	}
	store, err := k.readStore()
	if err != nil {
		return err
	}
	store.Entries[id] = env
	return k.writeStore(store)
}

func (k *Keystore) RemoveSecret(id string) (bool, error) {
	store, err := k.readStore()
	if err != nil {
		return false, err
	}
	if _, ok := store.Entries[id]; !ok {
		return false, nil
	}
	delete(store.Entries, id)
	return true, k.writeStore(store)
}

func (k *Keystore) ListSecretIDs() ([]string, error) {
	store, err := k.readStore()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(store.Entries))
	for id := range store.Entries {
		ids = append(ids, id)
	}
	return ids, nil
}

func (k *Keystore) readStore() (storeFile, error) {
	data, err := os.ReadFile(k.paths.SecretsFile)
	if errors.Is(err, os.ErrNotExist) {
		return emptyStore(), nil
	}
	if err != nil {
		return storeFile{}, err
	}
	var parsed storeFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return storeFile{}, err
	}
	if parsed.Version != fileVer || parsed.Entries == nil {
		return emptyStore(), nil
	}
	return parsed, nil
}

func (k *Keystore) writeStore(store storeFile) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(k.paths.SecretsFile, data, 0o600)
}

func emptyStore() storeFile {
	return storeFile{Version: fileVer, Entries: map[string]envelope{}}
}

func (k *Keystore) loadOrCreateSalt() ([]byte, error) {
	data, err := os.ReadFile(k.paths.KeystoreSaltFile)
	if err == nil && len(data) == keyLen {
		return data, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	salt := make([]byte, keyLen)
	if _, err := io.ReadFull(k.rand, salt); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(k.paths.KeystoreSaltFile, salt, 0o600); err != nil {
		return nil, err
	}
	return salt, nil
}

func (k *Keystore) deriveKey() ([]byte, error) {
	salt, err := k.loadOrCreateSalt()
	if err != nil {
		return nil, err
	}
	return pbkdf2.Key([]byte(k.seed), salt, pbkdf2Iter, keyLen, sha256.New), nil
}

func (k *Keystore) encrypt(key []byte, plaintext string) (envelope, error) {
	iv := make([]byte, ivLen)
	if _, err := io.ReadFull(k.rand, iv); err != nil {
		return envelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return envelope{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return envelope{}, err
	}
	sealed := aead.Seal(nil, iv, []byte(plaintext), nil)
	if len(sealed) < tagLen {
		return envelope{}, errors.New("encrypted payload shorter than GCM tag")
	}
	data := sealed[:len(sealed)-tagLen]
	tag := sealed[len(sealed)-tagLen:]
	return envelope{
		IV:   base64.StdEncoding.EncodeToString(iv),
		Data: base64.StdEncoding.EncodeToString(data),
		Tag:  base64.StdEncoding.EncodeToString(tag),
	}, nil
}

func decryptEnvelope(key []byte, env envelope) (string, error) {
	iv, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return "", err
	}
	tag, err := base64.StdEncoding.DecodeString(env.Tag)
	if err != nil {
		return "", err
	}
	if len(iv) != ivLen {
		return "", fmt.Errorf("invalid IV length")
	}
	if len(tag) != tagLen {
		return "", fmt.Errorf("invalid auth tag length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	opened, err := aead.Open(nil, iv, append(data, tag...), nil)
	if err != nil {
		return "", err
	}
	return string(opened), nil
}

func defaultSeed() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return host + "|" + u.Username, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
