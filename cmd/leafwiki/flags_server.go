package main

import "github.com/urfave/cli/v3"

// serverOptions holds the options for listener, data directory and logging.
type serverOptions struct {
	host              string
	port              string
	unixSocket        string
	dataDir           string
	basePath          string
	allowInsecure     bool
	noTLS             bool
	tlsCert           string
	tlsKey            string
	logFormat         string
	disableRequestLog bool

	// LLM (OpenAI-compatible) configuration for the "generate document from URL" feature.
	llmBaseURL string
	llmAPIKey  string
	llmModel   string
}

// Flags declares them. Adding an option here is the whole change: the flag,
// its default, its environment variable and its help text are one literal,
// and --help is generated from it.
func (o *serverOptions) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "host",
			Destination: &o.host,
			Category:    catServer,
			Usage:       "host/IP address to bind the server to (e.g. 127.0.0.1 or 0.0.0.0)",
			Value:       "127.0.0.1",
			Sources:     envVars("LEAFWIKI_HOST"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "port",
			Destination: &o.port,
			Category:    catServer,
			Usage:       "port to run the server on",
			Value:       "8080",
			Sources:     envVars("LEAFWIKI_PORT"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "unix-socket",
			Destination: &o.unixSocket,
			Category:    catServer,
			Usage:       "path to a unix domain socket to listen on; overrides --host and --port",
			Sources:     envVars("LEAFWIKI_UNIX_SOCKET"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "data-dir",
			Destination: &o.dataDir,
			Category:    catServer,
			Usage:       "path to data directory",
			Value:       "./data",
			Sources:     envVars("LEAFWIKI_DATA_DIR"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "base-path",
			Destination: &o.basePath,
			Category:    catServer,
			Usage:       "URL prefix when served behind a reverse proxy (e.g. /wiki)",
			Sources:     envVars("LEAFWIKI_BASE_PATH"),
			Config:      trimmed,
		},
		&cli.BoolFlag{
			Name:        "allow-insecure",
			Destination: &o.allowInsecure,
			Category:    catServer,
			Usage:       "allow insecure HTTP; auth cookies may then travel in plain text",
			Sources:     envBoolVars("LEAFWIKI_ALLOW_INSECURE"),
		},
		&cli.StringFlag{
			Name:        "tls-cert",
			Destination: &o.tlsCert,
			Category:    catServer,
			Usage:       "path to a TLS certificate file; enables native HTTPS. Requires --tls-key",
			Sources:     envVars("LEAFWIKI_TLS_CERT"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "tls-key",
			Destination: &o.tlsKey,
			Category:    catServer,
			Usage:       "path to a TLS private key file; enables native HTTPS. Requires --tls-cert",
			Sources:     envVars("LEAFWIKI_TLS_KEY"),
			Config:      trimmed,
		},
		&cli.BoolFlag{
			Name:        "no-tls",
			Destination: &o.noTLS,
			Category:    catServer,
			Usage:       "serve plain HTTP instead of the default HTTPS (HTTPS is used automatically when no --tls-cert/--tls-key is given)",
			Sources:     envBoolVars("LEAFWIKI_NO_TLS"),
		},
		&cli.StringFlag{
			Name:             "log-format",
			Destination:      &o.logFormat,
			Category:         catLogging,
			Usage:            "log output format: text or json",
			Value:            "text",
			Sources:          envVars("LEAFWIKI_LOG_FORMAT"),
			Validator:        validateLogFormatValue,
			ValidateDefaults: true,
			Config:           trimmed,
		},
		&cli.BoolFlag{
			Name:        "disable-request-log",
			Destination: &o.disableRequestLog,
			Category:    catLogging,
			Usage:       "suppress per-request HTTP access log lines",
			Sources:     envBoolVars("LEAFWIKI_DISABLE_REQUEST_LOG"),
		},
		&cli.StringFlag{
			Name:        "llm-base-url",
			Destination: &o.llmBaseURL,
			Category:    catLLM,
			Usage:       "base URL of an OpenAI-compatible LLM API (e.g. https://api.openai.com/v1); enables the 'generate document from URL' feature",
			Sources:     envVars("LEAFWIKI_LLM_BASE_URL"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "llm-api-key",
			Destination: &o.llmAPIKey,
			Category:    catLLM,
			Usage:       "API key for the OpenAI-compatible LLM API",
			Sources:     envVars("LEAFWIKI_LLM_API_KEY"),
			Config:      trimmed,
		},
		&cli.StringFlag{
			Name:        "llm-model",
			Destination: &o.llmModel,
			Category:    catLLM,
			Usage:       "model name to use for the OpenAI-compatible LLM API (e.g. gpt-4o-mini)",
			Sources:     envVars("LEAFWIKI_LLM_MODEL"),
			Config:      trimmed,
		},
	}
}
