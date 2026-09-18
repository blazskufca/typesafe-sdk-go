package typesafe

import (
	"errors"
	"io/fs"

	"github.com/joho/godotenv"
)

// defaultDotenvPath is the file [WithDotenv] reads when given no paths.
const defaultDotenvPath = ".env"

// WithDotenv reads settings from .env files, for the variables this SDK looks at:
// TYPESAFE_API_KEY, TYPESAFE_BASE_URL, TYPESAFE_DEFAULT_MODEL, and
// TYPESAFE_LOG_LEVEL.
//
//	client, err := typesafe.New(typesafe.WithDotenv())
//
// With no arguments it reads ".env" from the working directory and does not mind
// if the file is absent. Named paths must exist, and the first file to define a
// variable wins.
//
// A .env file is a fallback, never an override: a variable already set in the
// process environment, or supplied by an option such as [WithAPIKey], is left
// alone. Nothing is written back to the process environment, so this is safe to
// use from a library or a test.
//
// Parsing is done by
// github.com/joho/godotenv, so the format is the usual one — KEY=value per line,
// blank lines and #comments ignored, an optional "export " prefix, and values
// optionally wrapped in single or double quotes:
//
//	# .env
//	TYPESAFE_API_KEY="sk-..."
//	export TYPESAFE_DEFAULT_MODEL=jev-latest   # trailing comments are stripped
func WithDotenv(paths ...string) Option {
	return func(c *config) error {
		if len(paths) == 0 {
			c.dotenv = append(c.dotenv, dotenvFile{path: defaultDotenvPath, optional: true})
			return nil
		}
		for _, path := range paths {
			c.dotenv = append(c.dotenv, dotenvFile{path: path})
		}
		return nil
	}
}

// dotenvFile is one file to read, and whether its absence is acceptable.
type dotenvFile struct {
	path     string
	optional bool
}

// loadDotenv reads the requested files, keeping the first definition of each
// variable.
func loadDotenv(files []dotenvFile) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	values := make(map[string]string)
	for _, file := range files {
		// Read, not Load: the process environment is the caller's to manage.
		parsed, err := godotenv.Read(file.path)
		if err != nil {
			if file.optional && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, configErrorf("could not read %s: %v", file.path, err)
		}
		for name, value := range parsed {
			if _, seen := values[name]; !seen {
				values[name] = value
			}
		}
	}
	return values, nil
}
