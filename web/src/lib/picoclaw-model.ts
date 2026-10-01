// Keep this list aligned with the backend and PicoClaw's local providers.
const keylessProviders = new Set(['ollama', 'lmstudio', 'vllm']);

export function modelProviderAllowsEmptyApiKey(model: string): boolean {
  const identifier = model.trim();
  const slash = identifier.indexOf('/');
  return slash > 0 && keylessProviders.has(identifier.slice(0, slash).trim().toLowerCase());
}

export function modelConfigComplete(model: string, apiBase: string, apiKey: string): boolean {
  return Boolean(
    model.trim() && apiBase.trim() && (apiKey.trim() || modelProviderAllowsEmptyApiKey(model))
  );
}
