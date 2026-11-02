package i18n

import (
	"regexp"
	"testing"
)

func TestLoad(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.langs) < 2 {
		t.Fatalf("expected at least 2 languages, got %d", len(b.langs))
	}
	for _, lang := range Supported {
		if _, ok := b.langs[lang]; !ok {
			t.Errorf("missing language %q", lang)
		}
	}
}

func TestT_Simple(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	en := b.New("en")
	if got := en.T("output.error", "boom"); got != "ERROR: boom" {
		t.Errorf("en T(output.error, boom) = %q", got)
	}
	es := b.New("es")
	if got := es.T("output.error", "boom"); got != "ERROR: boom" {
		t.Errorf("es T(output.error, boom) = %q", got)
	}
}

func TestT_Fallback(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	loc := b.New("fr") // unsupported → falls back to "en"
	if loc.Lang() != Default {
		t.Errorf("expected fallback to %q, got %q", Default, loc.Lang())
	}
}

func TestT_MissingKey(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	loc := b.New("en")
	if got := loc.T("nonexistent.key"); got != "nonexistent.key" {
		t.Errorf("expected raw key for missing key, got %q", got)
	}
}

func TestDetectLang(t *testing.T) {
	tests := []struct {
		name    string
		cfgLang string
		envs    map[string]string
		want    string
	}{
		{"config wins", "es", nil, "es"},
		{"env wins over system", "", map[string]string{"BFLOW_LANG": "es"}, "es"},
		{"system locale", "", map[string]string{"LANG": "es_MX.UTF-8"}, "es"},
		{"default", "", nil, Default},
		{"normalize region", "en_US", nil, "en"},
		{"normalize encoding", "es_ES.UTF-8", nil, "es"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear relevant env vars
			for _, k := range []string{"BFLOW_LANG", "LC_MESSAGES", "LC_ALL", "LANG"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.envs {
				t.Setenv(k, v)
			}
			if got := DetectLang(tt.cfgLang); got != tt.want {
				t.Errorf("DetectLang(%q) = %q, want %q", tt.cfgLang, got, tt.want)
			}
		})
	}
}

// TestLocaleKeyParity ensures every key in en.json exists in es.json and vice versa.
func TestLocaleKeyParity(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	en := b.langs["en"]
	es := b.langs["es"]
	for k := range en {
		if _, ok := es[k]; !ok {
			t.Errorf("key %q exists in en.json but not in es.json", k)
		}
	}
	for k := range es {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q exists in es.json but not in en.json", k)
		}
	}
}

// TestLocaleVerbParity checks that Sprintf verb counts match between languages.
func TestLocaleVerbParity(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	verbRe := regexp.MustCompile(`%[+\-# 0]*[0-9]*\.?[0-9]*[vTtbcdoOqxXUeEfFgGsp]|%%`)
	for key, enVal := range b.langs["en"] {
		esVal, ok := b.langs["es"][key]
		if !ok {
			continue
		}
		enVerbs := verbRe.FindAllString(enVal, -1)
		esVerbs := verbRe.FindAllString(esVal, -1)
		if len(enVerbs) != len(esVerbs) {
			t.Errorf("key %q: en has %d verbs %v, es has %d verbs %v",
				key, len(enVerbs), enVerbs, len(esVerbs), esVerbs)
		}
	}
}

// TestAllKeysUsed is a placeholder: in the full extraction (PR2-4),
// this will verify that every key in the locale files is referenced in source.
func TestAllKeysUsed(t *testing.T) {
	t.Skip("will be enabled after string extraction in PR2-4")
}
