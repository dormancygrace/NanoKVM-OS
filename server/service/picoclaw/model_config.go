package picoclaw

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func extractPicoclawModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

	if index := strings.LastIndex(model, "/"); index >= 0 && index < len(model)-1 {
		return strings.TrimSpace(model[index+1:])
	}

	return model
}

var (
	picoclawProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	picoclawKnownProviders  = map[string]struct{}{
		"anthropic":         {},
		"azure":             {},
		"azure_openai":      {},
		"baichuan":          {},
		"bedrock":           {},
		"cerebras":          {},
		"cohere":            {},
		"dashscope":         {},
		"deepseek":          {},
		"fireworks_ai":      {},
		"gemini":            {},
		"google":            {},
		"groq":              {},
		"lmstudio":          {},
		"mistral":           {},
		"moonshot":          {},
		"ollama":            {},
		"openai":            {},
		"openai_compatible": {},
		"openrouter":        {},
		"qwen":              {},
		"siliconflow":       {},
		"together_ai":       {},
		"vertex_ai":         {},
		"vllm":              {},
		"volcengine":        {},
		"xai":               {},
		"zhipu":             {},
	}
	// picoclawKeylessProviders are the local inference servers PicoClaw talks
	// to without an API key (emptyAPIKeyAllowed in PicoClaw's provider table).
	// Ollama, LM Studio and vLLM serve an OpenAI-compatible API with no
	// authentication by default, so the integration must not demand a key.
	picoclawKeylessProviders = map[string]struct{}{
		"lmstudio": {},
		"ollama":   {},
		"vllm":     {},
	}
)

// picoclawModelProvider returns the lower-cased provider prefix of a
// provider/model identifier, or "" when there is none.
func picoclawModelProvider(model string) string {
	provider, _, ok := strings.Cut(strings.TrimSpace(model), "/")
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(provider))
}

// picoclawProviderAllowsEmptyAPIKey reports whether the model's provider works
// without an API key.
func picoclawProviderAllowsEmptyAPIKey(model string) bool {
	_, ok := picoclawKeylessProviders[picoclawModelProvider(model)]
	return ok
}

func validatePicoclawModelIdentifier(model string) (string, error) {
	model = strings.TrimSpace(model)
	provider, modelRef, ok := strings.Cut(model, "/")
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelRef = strings.TrimSpace(modelRef)

	if !ok || provider == "" || modelRef == "" {
		return "", fmt.Errorf("model identifier must use provider/model format")
	}
	if !picoclawProviderPattern.MatchString(provider) {
		return "", fmt.Errorf("model provider %q is invalid", provider)
	}
	if provider == "openao" {
		return "", fmt.Errorf("model provider %q is invalid; did you mean openai?", provider)
	}
	if _, ok := picoclawKnownProviders[provider]; !ok {
		return "", fmt.Errorf("model provider %q is not supported by this PicoClaw integration", provider)
	}

	modelName := extractPicoclawModelName(model)
	if modelName == "" {
		return "", fmt.Errorf("model identifier must include a model name")
	}
	return modelName, nil
}

func isPicoclawModelConfigured(cfg picoclawConfigFile, security picoclawSecurityConfig, modelName string) bool {
	if modelName == "" {
		return false
	}

	for _, model := range cfg.ModelList {
		if strings.TrimSpace(model.ModelName) != modelName {
			continue
		}
		if model.APIBase == "" {
			continue
		}
		if picoclawProviderAllowsEmptyAPIKey(model.Model) {
			return true
		}
		if securityHasModelAPIKeys(security, modelName) {
			return true
		}
		return configHasModelAPIKeys(model.APIKey, model.APIKeys)
	}

	return false
}

func configHasModelAPIKeys(apiKey string, apiKeys []string) bool {
	return strings.TrimSpace(apiKey) != "" || len(apiKeys) > 0
}

func securityHasModelAPIKeys(security picoclawSecurityConfig, modelName string) bool {
	if modelName == "" || len(security.ModelList) == 0 {
		return false
	}

	if entry, ok := security.ModelList[modelName]; ok && len(entry.APIKeys) > 0 {
		return true
	}

	prefix := modelName + ":"
	for key, entry := range security.ModelList {
		if strings.HasPrefix(key, prefix) && len(entry.APIKeys) > 0 {
			return true
		}
	}

	return false
}

type ModelConfigUpdateRequest struct {
	Model   string `json:"model"`
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key"`
}

func (s *Service) UpdateModelConfig(c *gin.Context) {
	s.ensureDependencies()
	var req ModelConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePicoclawError(c, newPicoclawError(CodeInvalidAction, "invalid model config payload"))
		return
	}

	var modelName string
	applied := s.applyConfigChange(c, "model config", "model_config", false, func() *PicoclawError {
		var err error
		modelName, err = updatePicoclawModelConfig(
			strings.TrimSpace(req.APIBase),
			strings.TrimSpace(req.APIKey),
			strings.TrimSpace(req.Model),
		)
		if err != nil {
			return newPicoclawError(CodeRuntimeUnavailable, err.Error())
		}
		if err := ensurePicoclawStartupDefaults(); err != nil {
			return newPicoclawError(CodeRuntimeUnavailable, "model config saved, but failed to persist picoclaw defaults: "+err.Error())
		}
		return nil
	})
	if !applied {
		return
	}

	writeSuccess(c, gin.H{
		"model_name": modelName,
		"status":     s.runtimeStatus(),
	})
}

// applyConfigChange runs change under the runtime lifecycle lock and restarts
// a running PicoClaw afterwards, so that it reads the new config. what names
// the change in restart errors. It writes the error response itself and
// reports whether the change was applied.
//
// A gateway keeps the tools of the config it started with (hot_reload is
// off). With revoke, the change takes privileges away: any live gateway,
// ready or not, is stopped and checked to be gone before success, also
// without the control lease (it is then not restarted) and also when change
// fails.
func (s *Service) applyConfigChange(c *gin.Context, what string, source string, revoke bool, change func() *PicoclawError) bool {
	ready := func(status RuntimeStatus) bool { return status.Ready || status.Status == "ready" }
	shouldRestart := ready(s.runtime.Get())
	var releaseControl func()
	defer func() {
		if releaseControl != nil {
			releaseControl()
		}
	}()
	acquire := func() bool {
		release, controlErr := s.acquireControlMode()
		if controlErr == nil {
			releaseControl = release
			return true
		}
		if revoke {
			shouldRestart = false
			return true
		}
		writePicoclawErrorWithData(c, controlErr, gin.H{"status": s.runtimeStatus()})
		return false
	}
	// Restart follows StartRuntime's lock order: stable PicoClaw control
	// lease first, then the runtime lifecycle lock around config write and
	// stop/start.
	if shouldRestart && !acquire() {
		return false
	}

	unlockLifecycle := s.lockRuntimeLifecycle()
	defer unlockLifecycle()
	if releaseControl == nil && ready(s.runtime.Get()) {
		shouldRestart = true
		if !acquire() {
			return false
		}
	}

	if err := change(); err != nil {
		if revoke {
			if stopErr := s.stopLiveGateway(); stopErr != nil {
				err = newPicoclawError(err.Code, err.Message+"; the running picoclaw gateway could not be stopped: "+stopErr.Message)
			}
		}
		writePicoclawError(c, err)
		return false
	}

	if shouldRestart {
		if stopErr := s.stopGatewayAndCheck(); stopErr != nil {
			writePicoclawError(c, newPicoclawError(CodeRuntimeUnavailable, what+" saved, but failed to restart picoclaw runtime: "+stopErr.Message))
			return false
		}
		if _, _, startErr := s.startRuntime(); startErr != nil {
			writePicoclawError(c, newPicoclawError(CodeRuntimeUnavailable, what+" saved, but failed to restart picoclaw runtime: "+startErr.Message))
			return false
		}
		s.setRuntimeIntentDesired(true, source)
		return true
	}
	if revoke {
		if stopErr := s.stopLiveGateway(); stopErr != nil {
			writePicoclawError(c, newPicoclawError(CodeRuntimeUnavailable, what+" saved, but the running picoclaw gateway could not be stopped: "+stopErr.Message))
			return false
		}
	}
	_ = s.syncConfigFromPicoclaw()
	_ = s.ensureRuntimeReadyForLifecycle()
	return true
}

// stopPicoclawGateway stops the gateway; a variable for tests.
var stopPicoclawGateway = func(s *Service) *PicoclawError {
	_, _, err := s.stopRuntime()
	return err
}

// gatewayExitWait bounds the wait for a stopped gateway to leave /proc: the
// stop script waits for TERM, but returns right after a KILL.
const gatewayExitWait = 3 * time.Second

// stopGatewayAndCheck stops the gateway and checks that no gateway process
// is left.
func (s *Service) stopGatewayAndCheck() *PicoclawError {
	if err := stopPicoclawGateway(s); err != nil {
		return err
	}
	for deadline := time.Now().Add(gatewayExitWait); ; time.Sleep(50 * time.Millisecond) {
		running, err := isRuntimeRunning()
		if err != nil {
			return newPicoclawError(CodeRuntimeUnavailable, "check picoclaw gateway process: "+err.Error())
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return newPicoclawError(CodeRuntimeUnavailable, "the picoclaw gateway is still running")
		}
	}
}

// stopLiveGateway stops a gateway process if one runs, ready or not.
func (s *Service) stopLiveGateway() *PicoclawError {
	running, err := isRuntimeRunning()
	if err != nil {
		return newPicoclawError(CodeRuntimeUnavailable, "check picoclaw gateway process: "+err.Error())
	}
	if !running {
		return nil
	}
	return s.stopGatewayAndCheck()
}

func updatePicoclawModelConfig(apiBase string, apiKey string, model string) (string, error) {
	if apiBase == "" {
		return "", fmt.Errorf("model api_base is required")
	}
	if model == "" {
		return "", fmt.Errorf("model identifier is required")
	}

	modelName, err := validatePicoclawModelIdentifier(model)
	if err != nil {
		return "", err
	}
	if apiKey == "" && !picoclawProviderAllowsEmptyAPIKey(model) {
		return "", fmt.Errorf("model api_key is required")
	}

	doc, err := loadOrInitializePicoclawConfigDocument()
	if err != nil {
		return "", err
	}

	doc.raw["version"] = currentPicoclawConfigVersion

	modelListValue, ok := doc.raw["model_list"].([]any)
	if !ok {
		modelListValue = []any{}
	}

	modelUpdated := false
	updatedModelIndex := -1
	for index, item := range modelListValue {
		modelMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		currentModelName := strings.TrimSpace(fmt.Sprintf("%v", modelMap["model_name"]))
		if currentModelName != modelName {
			continue
		}
		modelMap["model_name"] = modelName
		modelMap["model"] = model
		modelMap["api_base"] = apiBase
		delete(modelMap, "api_key")
		delete(modelMap, "api_keys")
		modelUpdated = true
		updatedModelIndex = index
		break
	}
	if !modelUpdated {
		modelListValue = append(modelListValue, map[string]any{
			"model_name": modelName,
			"model":      model,
			"api_base":   apiBase,
		})
		doc.raw["model_list"] = modelListValue
		updatedModelIndex = len(modelListValue) - 1
	}

	agents, ok := doc.raw["agents"].(map[string]any)
	if !ok {
		agents = map[string]any{}
		doc.raw["agents"] = agents
	}
	defaults, ok := agents["defaults"].(map[string]any)
	if !ok {
		defaults = map[string]any{}
		agents["defaults"] = defaults
	}
	defaults["model_name"] = modelName
	delete(defaults, "model")

	if err := doc.saveConfig(); err != nil {
		return "", err
	}
	if doc.security.ModelList == nil {
		doc.security.ModelList = map[string]picoclawModelSecurityEntry{}
	}
	securityModelName := indexedModelName(modelListValue, updatedModelIndex, modelName)
	if apiKey == "" {
		// A keyless provider: drop any key left from an earlier setup so
		// PicoClaw does not send a stale Authorization header.
		delete(doc.security.ModelList, securityModelName)
		// Older configs used an unindexed key as the model's fallback.
		delete(doc.security.ModelList, modelName)
	} else {
		doc.security.ModelList[securityModelName] = picoclawModelSecurityEntry{
			APIKeys: []string{apiKey},
		}
	}
	if err := doc.saveSecurity(); err != nil {
		return "", err
	}

	return modelName, nil
}

func indexedModelName(modelList []any, targetIndex int, modelName string) string {
	if targetIndex < 0 {
		return modelName
	}

	currentIndex := 0
	for index, item := range modelList {
		modelMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		currentModelName := strings.TrimSpace(fmt.Sprintf("%v", modelMap["model_name"]))
		if currentModelName != modelName {
			continue
		}
		if index == targetIndex {
			return fmt.Sprintf("%s:%d", modelName, currentIndex)
		}
		currentIndex++
	}

	return modelName
}
