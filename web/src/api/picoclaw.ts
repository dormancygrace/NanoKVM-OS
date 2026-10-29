import { http } from '@/lib/http.ts';
import type { PicoclawRuntimeStatus } from '@/jotai/picoclaw';

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

export function setPicoclawModelConfig(data: ModelConfigRequest) {
  return http.post<{ status: PicoclawRuntimeStatus; installed?: boolean }>(
    '/api/picoclaw/model/config',
    data
  );
}

// The agent's shell, file write and command scheduling tools on the NanoKVM.
export function setPicoclawHostTools(enabled: boolean) {
  return http.post<{ status: PicoclawRuntimeStatus; installed?: boolean }>(
    '/api/picoclaw/host-tools',
    { enabled }
  );
}

export function setPicoclawAgentProfile(data: AgentProfileRequest) {
  return http.post<{ status: PicoclawRuntimeStatus; installed?: boolean }>(
    '/api/picoclaw/agent/profile',
    data
  );
}

export function listPicoclawSessions(params?: { offset?: number; limit?: number }) {
  // http.get already wraps its argument as axios `params`.
  return http.get<PicoclawSessionListItem[]>('/api/picoclaw/sessions', params);
}

export function getPicoclawSession(id: string) {
  return http.get<{ id: string; messages: { role: 'user' | 'assistant'; content: string }[] }>(
    `/api/picoclaw/sessions/${encodeURIComponent(id)}`
  );
}

export function deletePicoclawSession(id: string) {
  return http.delete(`/api/picoclaw/sessions/${encodeURIComponent(id)}`);
}

export function getRuntimeStatus() {
  return http.get<PicoclawRuntimeStatus>('/api/picoclaw/runtime/status');
}

export function startRuntime() {
  return http.post<{ status: PicoclawRuntimeStatus }>('/api/picoclaw/runtime/start');
}

export function stopRuntime() {
  return http.post<{ status: PicoclawRuntimeStatus }>('/api/picoclaw/runtime/stop');
}

export function setAIControlMode(mode: 'off' | 'mcp' | 'picoclaw') {
  return http.request({
    method: 'put',
    url: '/api/ai/control/mode',
    data: { mode }
  });
}

export function installRuntime() {
  return http.post<{ status: PicoclawRuntimeStatus; installed?: boolean }>(
    '/api/picoclaw/runtime/install'
  );
}

export function uninstallRuntime() {
  return http.post<{ status: PicoclawRuntimeStatus; installed?: boolean }>(
    '/api/picoclaw/runtime/uninstall'
  );
}
