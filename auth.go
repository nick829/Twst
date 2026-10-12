package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sandertv/gophertunnel/minecraft/auth"
	"golang.org/x/oauth2"
)

type persistentTokenSource struct {
	mu     sync.Mutex
	source oauth2.TokenSource
	path   string
}

func loadAuthentication(ctx context.Context, path string) (*persistentTokenSource, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read saved Microsoft/Xbox authentication: %w", err)
	}
	if err == nil {
		var token oauth2.Token
		if err := json.Unmarshal(data, &token); err != nil || token.AccessToken == "" || token.RefreshToken == "" {
			return nil, fmt.Errorf("saved Microsoft/Xbox authentication file is invalid; remove %s and restart to authenticate again", path)
		}
		source := auth.RefreshTokenSource(&token)
		refreshed, refreshErr := source.Token()
		if refreshErr == nil {
			if err := saveToken(path, refreshed); err != nil {
				return nil, fmt.Errorf("save refreshed Microsoft/Xbox authentication: %w", err)
			}
			return &persistentTokenSource{source: source, path: path}, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !isRevokedToken(refreshErr) {
			return nil, errors.New("saved Microsoft/Xbox authentication could not be refreshed; check FeatherPanel network access and restart. Authentication details were not logged")
		}
		fmt.Fprintln(os.Stderr, "Saved Microsoft/Xbox authentication is expired or revoked. A new device-code sign-in is required.")
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	fmt.Fprintln(os.Stderr, "Microsoft/Xbox authentication is required. Follow the device-code prompt below.")
	token, err := auth.RequestLiveTokenContext(ctx, os.Stdout)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Microsoft/Xbox authentication failed. If saved credentials were expired or revoked, remove the configured AUTH_TOKEN_FILE and restart to authenticate again; authentication details were not logged")
	}
	if token == nil || token.AccessToken == "" || token.RefreshToken == "" {
		return nil, errors.New("Microsoft/Xbox authentication returned no reusable refresh token")
	}
	if err := saveToken(path, token); err != nil {
		return nil, fmt.Errorf("save Microsoft/Xbox authentication: %w", err)
	}
	return &persistentTokenSource{source: auth.RefreshTokenSource(token), path: path}, nil
}

func (source *persistentTokenSource) Token() (*oauth2.Token, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	token, err := source.source.Token()
	if err != nil {
		return nil, errors.New("Microsoft/Xbox authentication token refresh failed; authenticate again by removing AUTH_TOKEN_FILE and restarting")
	}
	if err := saveToken(source.path, token); err != nil {
		return nil, fmt.Errorf("persist refreshed Microsoft/Xbox authentication: %w", err)
	}
	return token, nil
}

func saveToken(path string, token *oauth2.Token) error {
	if token == nil {
		return errors.New("cannot save an empty authentication token")
	}
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".auth-token-*")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict temporary token file permissions: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write token file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync token file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close token file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	return nil
}

func isRevokedToken(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		return false
	}
	code := strings.ToLower(retrieveErr.ErrorCode)
	return code == "invalid_grant" || code == "invalid_token"
}
