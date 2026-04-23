package broker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// FileAuth implements Authenticator using credentials from a YAML or JSON file.
type FileAuth struct {
	mu        sync.RWMutex
	users     []UserCredential
	userIndex map[string]int // username -> index in users slice
}

// UserCredential represents a user entry in the auth file.
type UserCredential struct {
	Username  string   `json:"username" yaml:"username"`
	Password  string   `json:"password" yaml:"password"`
	ClientIDs []string `json:"client_ids" yaml:"client_ids"`
}

// AuthFile represents the structure of the auth credentials file.
type AuthFile struct {
	Users []UserCredential `json:"users" yaml:"users"`
}

// NewFileAuth creates a new file-based authenticator and loads credentials from the given path.
func NewFileAuth(filePath string) (*FileAuth, error) {
	fa := &FileAuth{
		userIndex: make(map[string]int),
	}
	if err := fa.LoadFile(filePath); err != nil {
		return nil, fmt.Errorf("file auth: %w", err)
	}
	return fa, nil
}

// LoadFile reloads credentials from the given file path.
func (f *FileAuth) LoadFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading auth file: %w", err)
	}

	var authFile AuthFile

	// Detect format by extension
	switch {
	case strings.HasSuffix(filePath, ".json"):
		if err := json.Unmarshal(data, &authFile); err != nil {
			return fmt.Errorf("parsing JSON auth file: %w", err)
		}
	case strings.HasSuffix(filePath, ".yaml"), strings.HasSuffix(filePath, ".yml"):
		if err := yaml.Unmarshal(data, &authFile); err != nil {
			return fmt.Errorf("parsing YAML auth file: %w", err)
		}
	default:
		// Try YAML by default
		if err := yaml.Unmarshal(data, &authFile); err != nil {
			return fmt.Errorf("parsing auth file (tried YAML): %w", err)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.users = authFile.Users
	f.userIndex = make(map[string]int, len(authFile.Users))
	for i, u := range authFile.Users {
		f.userIndex[u.Username] = i
	}

	return nil
}

// Authenticate checks the provided credentials against the loaded users.
func (f *FileAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	idx, ok := f.userIndex[username]
	if !ok {
		return ErrAuthFailed
	}

	user := f.users[idx]

	if subtle.ConstantTimeCompare([]byte(password), []byte(user.Password)) == 0 {
		return ErrAuthFailed
	}

	// Check client_id restriction if specified
	if len(user.ClientIDs) > 0 {
		allowed := false
		for _, cid := range user.ClientIDs {
			if cid == clientID {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrUnauthorized
		}
	}

	return nil
}

// Users returns a copy of the loaded user credentials (for inspection/testing).
func (f *FileAuth) Users() []UserCredential {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([]UserCredential, len(f.users))
	copy(result, f.users)
	return result
}


