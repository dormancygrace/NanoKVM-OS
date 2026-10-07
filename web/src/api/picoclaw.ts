import { http } from '@/lib/http.ts';

type ModelConfigRequest = {
  model: string;
  api_base: string;
  api_key: string;
};

type AgentProfileRequest = {
  profile: string;
};

export type PicoclawSessionListItem = {
  id: string;
  title: string;
  preview: string;
  message_count: number;
  created: string;
  updated: string;
};

export type PicoclawSessionDetailMessage = {
  role: 'user' | 'assistant';
  content: string;
};

export type PicoclawSessionDetail = {
  id: string;
  messages: PicoclawSessionDetailMessage[];
  summary?: string;
  created: string;
  updated: string;
};

export function setPicoclawModelConfig(data: ModelConfigRequest) {
  return http.post('/api/picoclaw/model/config', data);
}

export function setPicoclawAgentProfile(data: AgentProfileRequest) {
  return http.post('/api/picoclaw/agent/profile', data);
}

export function listPicoclawSessions(params?: { offset?: number; limit?: number }) {
  // http.get already wraps its argument as axios `params`.
  return http.get('/api/picoclaw/sessions', params);
}

export function getPicoclawSession(id: string) {
  return http.get(`/api/picoclaw/sessions/${encodeURIComponent(id)}`);
}

export function deletePicoclawSession(id: string) {
  return http.delete(`/api/picoclaw/sessions/${encodeURIComponent(id)}`);
}

export function getRuntimeStatus() {
  return http.get('/api/picoclaw/runtime/status');
}

export function startRuntime() {
  return http.post('/api/picoclaw/runtime/start');
}

export function stopRuntime() {
  return http.post('/api/picoclaw/runtime/stop');
}

export function getAIControlStatus() {
  return http.get('/api/ai/control/status');
}

export function setAIControlMode(mode: 'off' | 'mcp' | 'picoclaw') {
  return http.request({
    method: 'put',
    url: '/api/ai/control/mode',
    data: { mode }
  });
}

export function installRuntime() {
  return http.post('/api/picoclaw/runtime/install');
}

export function uninstallRuntime() {
  return http.post('/api/picoclaw/runtime/uninstall');
}
