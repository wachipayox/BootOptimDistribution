package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wachipayox/BootOptimDistribution/internal/profileapi"
)

type releaseKeySet map[string]ed25519.PublicKey

func loadReleaseKeys(path string) (releaseKeySet, error) {
	if strings.TrimSpace(path) == "" {
		return releaseKeySet{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release public keys: %w", err)
	}
	var encoded map[string]string
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return nil, fmt.Errorf("decode release public keys: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("release public key file must contain one JSON object")
	}
	if len(encoded) == 0 {
		return nil, errors.New("release public key file must contain at least one key")
	}
	keys := make(releaseKeySet, len(encoded))
	for id, value := range encoded {
		if len(id) < 1 || len(id) > 128 || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("invalid release key id %q", id)
		}
		key, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("release public key %q must be 32-byte unpadded base64url", id)
		}
		keys[id] = ed25519.PublicKey(key)
	}
	return keys, nil
}

func (keys releaseKeySet) PublicKey(_ context.Context, id string) (ed25519.PublicKey, error) {
	key, ok := keys[id]
	if !ok {
		return nil, profileapi.ErrKeyNotFound
	}
	return append(ed25519.PublicKey(nil), key...), nil
}
