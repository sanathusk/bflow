package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed locales/*.json
var localeFS embed.FS

// Supported lists the available locale codes.
var Supported = []string{"en", "es"}

// Default is the fallback language for new installs.
const Default = "en"

// Bundle holds all loaded translations.
type Bundle struct {
	langs    map[string]map[string]string
	fallback string
}

var defaultBundle *Bundle

func init() {
	defaultBundle, _ = Load()
}

// DefaultBundle returns the package-level embedded translation bundle.
func DefaultBundle() *Bundle {
	return defaultBundle
}

// DefaultLocalizer returns a localizer initialized with the default language.
func DefaultLocalizer() *Localizer {
	if defaultBundle != nil {
		return defaultBundle.New(Default)
	}
	return &Localizer{lang: Default}
}

// Msg translates key using the default language.
func Msg(key string, args ...any) string {
	return DefaultLocalizer().Msg(key, args...)
}

// T is an alias for Msg.
func T(key string, args ...any) string {
	return Msg(key, args...)
}

// Load reads the embedded locale files and returns a Bundle.
func Load() (*Bundle, error) {
	b := &Bundle{langs: map[string]map[string]string{}, fallback: Default}
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		data, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("i18n: %w", err)
		}
		var kv map[string]string
		if err := json.Unmarshal(data, &kv); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", e.Name(), err)
		}
		b.langs[lang] = kv
	}
	return b, nil
}

// Localizer translates message keys for one language.
type Localizer struct {
	lang   string
	bundle *Bundle
}

// New creates a Localizer for the given language tag (e.g. "en", "es").
// If the language is not available, it falls back to Default.
func (b *Bundle) New(lang string) *Localizer {
	if _, ok := b.langs[lang]; !ok {
		lang = b.fallback
	}
	return &Localizer{lang: lang, bundle: b}
}

// T translates key, interpolating args via fmt.Sprintf if present.
// Fallback chain: requested lang → Default. If missing in both, returns the key itself.
func (l *Localizer) T(key string, args ...any) string {
	pattern := ""
	if l != nil && l.bundle != nil {
		if m, ok := l.bundle.langs[l.lang]; ok {
			if s, ok := m[key]; ok {
				pattern = s
			}
		}
		if pattern == "" && l.lang != l.bundle.fallback {
			if m, ok := l.bundle.langs[l.bundle.fallback]; ok {
				if s, ok := m[key]; ok {
					pattern = s
				}
			}
		}
	}
	if pattern == "" {
		if len(args) > 0 {
			return key + ": " + fmt.Sprint(args...)
		}
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(pattern, args...)
	}
	return pattern
}

// Msg is an alias for T.
func (l *Localizer) Msg(key string, args ...any) string {
	return l.T(key, args...)
}

// Lang returns the active language code.
func (l *Localizer) Lang() string { return l.lang }

// DetectLang picks the language from config > env > system locale > default.
func DetectLang(cfgLang string) string {
	if cfgLang != "" {
		return normalize(cfgLang)
	}
	if v := os.Getenv("BFLOW_LANG"); v != "" {
		return normalize(v)
	}
	for _, k := range []string{"LC_MESSAGES", "LC_ALL", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return normalize(v)
		}
	}
	return Default
}

func normalize(tag string) string {
	tag = strings.SplitN(tag, ".", 2)[0] // en_US.UTF-8 → en_US
	tag = strings.SplitN(tag, "_", 2)[0] // en_US → en
	return strings.ToLower(tag)
}
