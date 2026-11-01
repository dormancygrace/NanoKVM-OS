package picoclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePicoclawModelConfigInitializesMissingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)

	previousOnboard := runPicoclawOnboardForConfig
	onboardCalled := false
	runPicoclawOnboardForConfig = func() (string, *PicoclawError) {
		onboardCalled = true
		configPath := filepath.Join(home, "config.json")
		err := os.WriteFile(configPath, []byte(`{
  "agents": {
    "defaults": {}
  },
  "gateway": {
    "host": "127.0.0.1",
    "port": 18790
  },
  "model_list": [],
  "channel_list": {}
}`), 0o600)
		if err != nil {
			return "", newPicoclawError(CodeRuntimeUnavailable, err.Error())
		}
		return "initialized", nil
	}
	t.Cleanup(func() {
		runPicoclawOnboardForConfig = previousOnboard
	})

	modelName, err := updatePicoclawModelConfig(
		"https://api.example.invalid",
		"secret-key",
		"openai/test-model",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !onboardCalled {
		t.Fatal("missing config did not trigger PicoClaw onboard")
	}
	if modelName != "test-model" {
		t.Fatalf("model name = %q, want test-model", modelName)
	}

	doc, err := loadPicoclawConfigDocument()
	if err != nil {
		t.Fatal(err)
	}
	if doc.config.Agents.Defaults.ModelName != "test-model" {
		t.Fatalf("default model = %q, want test-model", doc.config.Agents.Defaults.ModelName)
	}
	if version, ok := doc.raw["version"].(float64); !ok || int(version) != currentPicoclawConfigVersion {
		t.Fatalf("config version = %v, want %d", doc.raw["version"], currentPicoclawConfigVersion)
	}
	if !isPicoclawModelConfigured(doc.config, doc.security, "test-model") {
		t.Fatalf("model was not configured: config=%+v security=%+v", doc.config.ModelList, doc.security.ModelList)
	}
	if len(doc.config.ModelList) != 1 || doc.config.ModelList[0].APIKey != "" || len(doc.config.ModelList[0].APIKeys) != 0 {
		t.Fatalf("model API key leaked into config.json: %+v", doc.config.ModelList)
	}
}

func TestUpdatePicoclawModelConfigMigratesVersionAndKeepsUnknownFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)

	configPath := filepath.Join(home, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "version": 4,
  "unknown_top_level": {
    "keep": true
  },
  "agents": {
    "defaults": {
      "model_name": "old-model"
    }
  },
  "gateway": {
    "host": "127.0.0.1",
    "port": 18790
  },
  "model_list": [
    {
      "model_name": "old-model",
      "model": "openai/old-model",
      "api_base": "https://api.example.invalid"
    }
  ],
  "channel_list": {}
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	modelName, err := updatePicoclawModelConfig(
		"https://api.example.invalid",
		"secret-key",
		"openai/new-model",
	)
	if err != nil {
		t.Fatal(err)
	}
	if modelName != "new-model" {
		t.Fatalf("model name = %q, want new-model", modelName)
	}

	doc, err := loadPicoclawConfigDocument()
	if err != nil {
		t.Fatal(err)
	}
	if version, ok := doc.raw["version"].(float64); !ok || int(version) != currentPicoclawConfigVersion {
		t.Fatalf("config version = %v, want %d", doc.raw["version"], currentPicoclawConfigVersion)
	}
	unknown, ok := doc.raw["unknown_top_level"].(map[string]any)
	if !ok || unknown["keep"] != true {
		t.Fatalf("unknown fields were not preserved: %#v", doc.raw["unknown_top_level"])
	}
}

func TestUpdatePicoclawModelConfigAllowsKeylessLocalProvider(t *testing.T) {
	for _, provider := range []string{"ollama", "lmstudio", "vllm"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PICOCLAW_HOME", home)
			const modelName = "local-model"
			model := provider + "/" + modelName
			configPath := filepath.Join(home, "config.json")
			if err := os.WriteFile(configPath, []byte(`{
  "agents": {"defaults": {}},
  "gateway": {"host": "127.0.0.1", "port": 18790},
  "model_list": [],
  "channel_list": {}
}`), 0o600); err != nil {
				t.Fatal(err)
			}
			// Both inline and legacy fallback keys must disappear on a keyless save.
			if _, err := updatePicoclawModelConfig("http://192.0.2.10:11434/v1", "old-key", model); err != nil {
				t.Fatal(err)
			}
			doc, err := loadPicoclawConfigDocument()
			if err != nil {
				t.Fatal(err)
			}
			entry := doc.raw["model_list"].([]any)[0].(map[string]any)
			entry["api_key"] = "inline-key"
			entry["api_keys"] = []string{"inline-rotated-key"}
			doc.security.ModelList[modelName] = picoclawModelSecurityEntry{APIKeys: []string{"legacy-key"}}
			doc.security.ModelList["unrelated:0"] = picoclawModelSecurityEntry{APIKeys: []string{"keep-key"}}
			if err := doc.saveConfig(); err != nil {
				t.Fatal(err)
			}
			if err := doc.saveSecurity(); err != nil {
				t.Fatal(err)
			}
			gotName, err := updatePicoclawModelConfig("http://192.0.2.10:11434/v1", "", model)
			if err != nil {
				t.Fatalf("keyless %s model rejected: %v", provider, err)
			}
			if gotName != modelName {
				t.Fatalf("model name = %q, want %q", gotName, modelName)
			}
			doc, err = loadPicoclawConfigDocument()
			if err != nil {
				t.Fatal(err)
			}
			if !isPicoclawModelConfigured(doc.config, doc.security, modelName) {
				t.Fatal("keyless local model not reported as configured")
			}
			if securityHasModelAPIKeys(doc.security, modelName) {
				t.Fatal("stale security API key kept for keyless model")
			}
			if len(doc.config.ModelList) != 1 || configHasModelAPIKeys(doc.config.ModelList[0].APIKey, doc.config.ModelList[0].APIKeys) {
				t.Fatal("stale inline API key kept for keyless model")
			}
			if !securityHasModelAPIKeys(doc.security, "unrelated") {
				t.Fatal("another model's API key was removed")
			}
			// Local servers may still opt in to authentication.
			if _, err := updatePicoclawModelConfig("http://192.0.2.10:11434/v1", "new-key", model); err != nil {
				t.Fatal(err)
			}
			doc, err = loadPicoclawConfigDocument()
			if err != nil {
				t.Fatal(err)
			}
			keys := doc.security.ModelList[modelName+":0"].APIKeys
			if len(keys) != 1 || keys[0] != "new-key" {
				t.Fatal("explicit local API key was not restored")
			}
		})
	}
}

func TestUpdatePicoclawModelConfigRequiresKeyForHostedProvider(t *testing.T) {
	for _, provider := range []string{"openai", "openai_compatible", "anthropic", "gemini", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PICOCLAW_HOME", home)
			_, err := updatePicoclawModelConfig("https://api.example.invalid", "", provider+"/test-model")
			if err == nil || !strings.Contains(err.Error(), "api_key is required") {
				t.Fatalf("error = %v, want api_key required", err)
			}
			if _, err := os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
				t.Fatal("rejected request changed config")
			}
		})
	}
}

func TestPicoclawProviderAllowsEmptyAPIKey(t *testing.T) {
	cases := map[string]bool{
		"ollama/qwen3.5:9b":    true,
		" Ollama/llama3 ":      true,
		"lmstudio/local-model": true,
		"vllm/meta-llama/x":    true,
		"openai/gpt-5.4":       false,
		"openai_compatible/x":  false,
		"qwen3.5:9b":           false,
		"":                     false,
	}
	for model, want := range cases {
		if got := picoclawProviderAllowsEmptyAPIKey(model); got != want {
			t.Errorf("picoclawProviderAllowsEmptyAPIKey(%q) = %v, want %v", model, got, want)
		}
	}
}
