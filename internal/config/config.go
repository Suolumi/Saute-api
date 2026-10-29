package config

import (
	"fmt"
	"net/url"
	"recipes/internal/mail_sender"
	"recipes/internal/translator"
	"recipes/internal/utils"
	"strings"
	"time"
)

type MCPConfig struct {
	PublicURL              string
	JWTSecret              string
	TokenExpiration        time.Duration
	MaxDecodedPictureBytes int
}

type JwtConfig struct {
	AccessSecret      string
	AccessExpiration  time.Duration
	RefreshSecret     string
	RefreshExpiration time.Duration
	ResetSecret       string
	ResetExpiration   time.Duration
}

type DatabaseConfig struct {
	DefaultAddr   string
	Name          string
	Timeout       time.Duration
	AdminUsername string
	AdminMail     string
	AdminPassword string
}

type RuntimeConfig struct {
	Port           int
	WebappUrl      string
	ImagesDir      string
	RecipeImageDir string
}

type Config struct {
	Db         *DatabaseConfig
	Jwt        *JwtConfig
	Cfg        *RuntimeConfig
	Translator *translator.TranslatorConfig
	Mails      *mail_sender.MailSenderConfig
	MCP        *MCPConfig

	// TranslationLocales is Translator.Locales parsed and normalized. Empty
	// disables translate-on-write. Populated by NewConfig, not from the env.
	TranslationLocales []string
}

// parseLocales splits a comma-separated locale list, normalizing each tag and
// rejecting any it cannot parse. Blank entries are ignored; duplicates collapse.
func parseLocales(raw string) ([]string, error) {
	seen := make(map[string]bool)
	var locales []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		normalized, err := utils.ParseLocale(part)
		if err != nil {
			return nil, fmt.Errorf("invalid locale %q", part)
		}
		if !seen[normalized] {
			seen[normalized] = true
			locales = append(locales, normalized)
		}
	}
	return locales, nil
}

func NewConfig(prefix ...string) (*Config, error) {
	cfg := Config{
		Db: &DatabaseConfig{
			DefaultAddr:   "mongodb://localhost:27017",
			Name:          "Recipes",
			Timeout:       time.Second * 2,
			AdminUsername: "admin",
			AdminMail:     "admin@admin.admin",
			AdminPassword: "admin",
		},
		Jwt: &JwtConfig{
			AccessSecret:      "",
			AccessExpiration:  time.Hour,
			RefreshSecret:     "",
			RefreshExpiration: time.Hour * 24 * 7 * 30,
			ResetSecret:       "",
			ResetExpiration:   time.Hour * 24 * 2,
		},
		Cfg: &RuntimeConfig{
			Port:           8080,
			WebappUrl:      "http://localhost:5173",
			ImagesDir:      "assets/pictures",
			RecipeImageDir: "assets/recipeImages",
		},
		Mails: &mail_sender.MailSenderConfig{
			Host:     "",
			Email:    "",
			Password: "",
			MailsDir: "assets/mails",
		},
		Translator: &translator.TranslatorConfig{
			ApiKey:  "",
			Locales: "en,fr,fi,zh",
		},
		MCP: &MCPConfig{
			TokenExpiration:        365 * 24 * time.Hour,
			MaxDecodedPictureBytes: 64 << 20,
		},
	}

	err := utils.LoadConfigFromEnv(&cfg, prefix...)
	if err != nil {
		return nil, err
	}

	cfg.TranslationLocales, err = parseLocales(cfg.Translator.Locales)
	if err != nil {
		return nil, fmt.Errorf("RECIPES_TRANSLATOR_LOCALES: %w", err)
	}

	if cfg.MCP.PublicURL == "" {
		cfg.MCP.PublicURL = fmt.Sprintf("http://localhost:%d/mcp", cfg.Cfg.Port)
	}
	parsed, err := url.Parse(cfg.MCP.PublicURL)
	if err != nil || parsed.Host == "" || parsed.Path != "/mcp" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return nil, fmt.Errorf("RECIPES_MCP_PUBLICURL must be an absolute URL ending in /mcp")
	}
	local := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if (local && parsed.Scheme != "http" && parsed.Scheme != "https") || (!local && parsed.Scheme != "https") {
		return nil, fmt.Errorf("RECIPES_MCP_PUBLICURL must use https outside localhost")
	}
	if !local && len(cfg.MCP.JWTSecret) < 32 {
		return nil, fmt.Errorf("RECIPES_MCP_JWTSECRET must contain at least 32 bytes")
	}

	return &cfg, nil
}
