package auth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dorkitude/linctl/pkg/api"
	"github.com/fatih/color"
	"golang.org/x/term"
)

// passEntryName returns the pass entry name to use, or "" if pass storage
// is not configured. Setting LINCTL_PASS_NAME=<entry> opts the user into
// storing the API key in `pass` instead of the JSON config file.
func passEntryName() string {
	return strings.TrimSpace(os.Getenv("LINCTL_PASS_NAME"))
}

var runPassCommand = func(stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.Command("pass", args...)
	cmd.Stdin = stdin
	return cmd.CombinedOutput()
}

var stdinIsTerminal = term.IsTerminal
var readTerminalPassword = term.ReadPassword

func readFromPass(name string) (string, error) {
	out, err := runPassCommand(nil, "show", "--", name)
	if err != nil {
		if strings.TrimSpace(string(out)) != "" {
			return "", fmt.Errorf("pass show %s: %s", name, strings.TrimSpace(string(out)))
		}
		return "", fmt.Errorf("pass show %s: %w", name, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			return line, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func writeToPass(name, value string) error {
	out, err := runPassCommand(strings.NewReader(value+"\n"), "insert", "-m", "-f", "--", name)
	if err != nil {
		if strings.TrimSpace(string(out)) == "" {
			return fmt.Errorf("pass insert %s: %w", name, err)
		}
		return fmt.Errorf("pass insert %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeFromPass(name string) error {
	out, err := runPassCommand(nil, "rm", "-f", "--", name)
	if err != nil {
		if strings.TrimSpace(string(out)) == "" {
			return fmt.Errorf("pass rm %s: %w", name, err)
		}
		return fmt.Errorf("pass rm %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

type AuthConfig struct {
	APIKey string `json:"api_key,omitempty"`
}

// getConfigPath returns the user-scoped path for the credential file.
func getConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".linctl-auth.json"), nil
}

// saveAuth writes credentials to a user-only file and repairs permissions on
// existing files. A parent directory created here receives user-only access.
func saveAuth(config AuthConfig) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}

	parentDir := filepath.Dir(configPath)
	parentCreated := false
	if _, err := os.Stat(parentDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(parentDir, 0700); err != nil {
			return err
		}
		parentCreated = true
	}
	if parentCreated {
		if err := os.Chmod(parentDir, 0700); err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	return os.Chmod(configPath, 0600)
}

func removeAuthConfig() error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loadAuth loads authentication credentials
func loadAuth() (*AuthConfig, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not authenticated")
		}
		return nil, err
	}

	var config AuthConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// GetAuthHeader returns the authorization header value.
// Precedence: LINCTL_API_KEY env var > pass (when LINCTL_PASS_NAME set) > config file.
func GetAuthHeader() (string, error) {
	if envKey := strings.TrimSpace(os.Getenv("LINCTL_API_KEY")); envKey != "" {
		return envKey, nil
	}

	if entry := passEntryName(); entry != "" {
		key, err := readFromPass(entry)
		if err != nil {
			return "", err
		}
		if key != "" {
			return key, nil
		}
	}

	config, err := loadAuth()
	if err != nil {
		return "", err
	}

	if config.APIKey != "" {
		return config.APIKey, nil
	}

	return "", fmt.Errorf("no valid authentication found")
}

// Login authenticates with a Personal API Key read from standard input.
func Login(plaintext, jsonOut bool) error {
	return loginWithAPIKey(plaintext, jsonOut)
}

// readAPIKey disables terminal echo for interactive input and preserves pipe
// input for automation. Standard input is the credential disclosure boundary.
func readAPIKey() (string, error) {
	if stdinIsTerminal(int(os.Stdin.Fd())) {
		value, err := readTerminalPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(value)), nil
	}

	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// loginWithAPIKey verifies and stores a Personal API Key without echoing
// terminal input. Piped input remains available for non-interactive login.
func loginWithAPIKey(plaintext, jsonOut bool) error {
	if !plaintext && !jsonOut {
		fmt.Println("\n" + color.New(color.FgYellow).Sprint("📝 Personal API Key Authentication"))
		fmt.Println("Get your API key from: https://linear.app/<your-org>/settings/account/security")

		var location string
		if entry := passEntryName(); entry != "" {
			location = fmt.Sprintf("pass entry %q", entry)
		} else {
			configPath, _ := getConfigPath()
			location = configPath
		}
		fmt.Printf("Your credentials will be stored in: %s\n", color.New(color.FgCyan).Sprint(location))
		fmt.Print("\nEnter your Personal API Key: ")
	}

	apiKey, err := readAPIKey()
	if err != nil {
		return err
	}

	if apiKey == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	// Test the API key
	client := api.NewClient(apiKey)
	user, err := client.GetViewer(context.Background())
	if err != nil {
		return fmt.Errorf("invalid API key: %v", err)
	}

	if entry := passEntryName(); entry != "" {
		if err := writeToPass(entry, apiKey); err != nil {
			return err
		}
		if err := removeAuthConfig(); err != nil {
			return err
		}
	} else {
		if err := saveAuth(AuthConfig{APIKey: apiKey}); err != nil {
			return err
		}
	}

	if !plaintext && !jsonOut {
		fmt.Printf("\n%s Authenticated as %s (%s)\n",
			color.New(color.FgGreen).Sprint("✅"),
			color.New(color.FgCyan).Sprint(user.Name),
			color.New(color.FgCyan).Sprint(user.Email))
	}

	return nil
}

// GetCurrentUser returns the current authenticated user
func GetCurrentUser() (*User, error) {
	authHeader, err := GetAuthHeader()
	if err != nil {
		return nil, err
	}

	client := api.NewClient(authHeader)
	apiUser, err := client.GetViewer(context.Background())
	if err != nil {
		return nil, err
	}

	// Convert api.User to auth.User
	return &User{
		ID:        apiUser.ID,
		Name:      apiUser.Name,
		Email:     apiUser.Email,
		AvatarURL: apiUser.AvatarURL,
	}, nil
}

// Logout clears stored credentials. When LINCTL_PASS_NAME is set, the pass
// entry is removed; the legacy JSON file is also removed if present so a
// future re-login starts from a clean slate.
func Logout() error {
	var passErr error
	if entry := passEntryName(); entry != "" {
		if err := removeFromPass(entry); err != nil {
			passErr = err
		}
	}

	return errors.Join(passErr, removeAuthConfig())
}
