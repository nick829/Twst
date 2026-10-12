package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

func TestSaveTokenUsesRestrictivePermissionsAndCanBeLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	token := &oauth2.Token{AccessToken: "access", RefreshToken: "refresh"}
	if err := saveToken(path, token); err != nil {
		t.Fatalf("save token: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token permissions = %04o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	var loaded oauth2.Token
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if loaded.AccessToken != token.AccessToken || loaded.RefreshToken != token.RefreshToken {
		t.Fatal("saved token did not round-trip")
	}
}

func TestIsRevokedToken(t *testing.T) {
	for _, test := range []struct {
		code string
		want bool
	}{
		{code: "invalid_grant", want: true},
		{code: "invalid_token", want: true},
		{code: "temporarily_unavailable", want: false},
	} {
		err := &oauth2.RetrieveError{ErrorCode: test.code}
		if got := isRevokedToken(err); got != test.want {
			t.Errorf("isRevokedToken(%q) = %t, want %t", test.code, got, test.want)
		}
	}
	if isRevokedToken(errors.New("network failure")) {
		t.Fatal("ordinary errors must not be treated as revoked credentials")
	}
}
