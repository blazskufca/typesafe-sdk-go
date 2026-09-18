package typesafe

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDotenv writes a .env file in a temporary directory and returns its path.
func writeDotenv(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestDotenvSuppliesSettings(t *testing.T) {
	clearEnv(t)
	path := writeDotenv(t, ".env", `
# The API key lives here.
TYPESAFE_API_KEY="sk-from-dotenv"
export TYPESAFE_DEFAULT_MODEL=jev-from-dotenv   # trailing comment
TYPESAFE_LOG_LEVEL='warn'
`)
	client, err := New(WithDotenv(path))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.cfg.apiKey != "sk-from-dotenv" {
		t.Errorf("apiKey = %q", client.cfg.apiKey)
	}
	if client.cfg.model != "jev-from-dotenv" {
		t.Errorf("model = %q", client.cfg.model)
	}
	if !client.cfg.logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("the log level from .env was not applied")
	}
	if client.cfg.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want the default", client.cfg.baseURL)
	}
}

func TestDotenvIsOnlyAFallback(t *testing.T) {
	clearEnv(t)
	path := writeDotenv(t, ".env", "TYPESAFE_API_KEY=from-dotenv\nTYPESAFE_DEFAULT_MODEL=model-from-dotenv\n")

	t.Run("the environment wins", func(t *testing.T) {
		t.Setenv(APIKeyEnv, "from-env")
		client, err := New(WithDotenv(path))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if client.cfg.apiKey != "from-env" {
			t.Errorf("apiKey = %q, want from-env", client.cfg.apiKey)
		}
		// A variable the environment does not set still comes from the file.
		if client.cfg.model != "model-from-dotenv" {
			t.Errorf("model = %q", client.cfg.model)
		}
	})

	t.Run("an option wins", func(t *testing.T) {
		t.Setenv(APIKeyEnv, "from-env")
		client, err := New(WithDotenv(path), WithAPIKey("from-option"))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if client.cfg.apiKey != "from-option" {
			t.Errorf("apiKey = %q, want from-option", client.cfg.apiKey)
		}
	})

	t.Run("option order does not matter", func(t *testing.T) {
		client, err := New(WithAPIKey("from-option"), WithDotenv(path))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if client.cfg.apiKey != "from-option" {
			t.Errorf("apiKey = %q, want from-option", client.cfg.apiKey)
		}
	})
}

func TestDotenvDoesNotTouchTheProcessEnvironment(t *testing.T) {
	clearEnv(t)
	path := writeDotenv(t, ".env", "TYPESAFE_API_KEY=from-dotenv\n")
	if _, err := New(WithDotenv(path)); err != nil {
		t.Fatalf("New: %v", err)
	}
	if value := os.Getenv(APIKeyEnv); value != "" {
		t.Errorf("%s was set in the process environment: %q", APIKeyEnv, value)
	}
}

func TestDotenvDefaultPath(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())

	// Absent by default: WithDotenv() is a convenience, not a requirement.
	if _, err := New(WithDotenv()); !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), APIKeyEnv) {
		t.Errorf("New with no .env = %v, want a missing-key error", err)
	}

	if err := os.WriteFile(".env", []byte("TYPESAFE_API_KEY=from-cwd\n"), 0o600); err != nil {
		t.Fatalf("writing .env: %v", err)
	}
	client, err := New(WithDotenv())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.cfg.apiKey != "from-cwd" {
		t.Errorf("apiKey = %q", client.cfg.apiKey)
	}
}

func TestDotenvNamedFileMustExist(t *testing.T) {
	clearEnv(t)
	missing := filepath.Join(t.TempDir(), "absent.env")
	_, err := New(WithDotenv(missing), WithAPIKey("k"))
	if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "absent.env") {
		t.Errorf("New = %v, want a missing-file error", err)
	}
}

func TestDotenvFirstFileWins(t *testing.T) {
	clearEnv(t)
	first := writeDotenv(t, "first.env", "TYPESAFE_API_KEY=first\n")
	second := writeDotenv(t, "second.env", "TYPESAFE_API_KEY=second\nTYPESAFE_DEFAULT_MODEL=from-second\n")

	client, err := New(WithDotenv(first, second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.cfg.apiKey != "first" {
		t.Errorf("apiKey = %q, want first", client.cfg.apiKey)
	}
	if client.cfg.model != "from-second" {
		t.Errorf("model = %q, want from-second", client.cfg.model)
	}
}

func TestDotenvFormat(t *testing.T) {
	clearEnv(t)
	path := writeDotenv(t, ".env", `
# a comment

KEY=plain
SPACED  =  padded
QUOTED="double quoted"
SINGLE='single quoted'
export EXPORTED=exported
EMPTY=
HASH_IN_QUOTES="a # b"
INLINE=value # trailing comment
ESCAPES="line\nbreak"
EQUALS=a=b=c
`)
	values, err := loadDotenv([]dotenvFile{{path: path}})
	if err != nil {
		t.Fatalf("loadDotenv: %v", err)
	}
	for name, want := range map[string]string{
		"KEY":            "plain",
		"SPACED":         "padded",
		"QUOTED":         "double quoted",
		"SINGLE":         "single quoted",
		"EXPORTED":       "exported",
		"EMPTY":          "",
		"HASH_IN_QUOTES": "a # b",
		"INLINE":         "value",
		"ESCAPES":        "line\nbreak",
		"EQUALS":         "a=b=c",
	} {
		if got := values[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, present := values["# a comment"]; present {
		t.Error("a comment was parsed as a variable")
	}
}

func TestDotenvRejectsMalformedFiles(t *testing.T) {
	clearEnv(t)
	// godotenv is the parser, so what counts as malformed is its call; a line with
	// no "=" is the case that matters here.
	for name, content := range map[string]string{
		"no equals sign": "TYPESAFE_API_KEY sk-123\n",
	} {
		path := writeDotenv(t, ".env", content)
		if _, err := loadDotenv([]dotenvFile{{path: path}}); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: err = %v, want ErrConfig", name, err)
		}
	}
}

func TestDotenvIsNotACallOption(t *testing.T) {
	clearEnv(t)
	path := writeDotenv(t, ".env", "TYPESAFE_DEFAULT_MODEL=ignored\n")
	client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"models": []}`, nil))

	_, err := client.Models.List(context.Background(), WithDotenv(path))
	if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "WithDotenv") {
		t.Errorf("err = %v, want an explanation that WithDotenv belongs in New", err)
	}
	if rec.Count() != 0 {
		t.Error("a request was sent")
	}
}
