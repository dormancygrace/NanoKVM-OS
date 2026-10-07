// Gateway helpers live apart from the REST calls in ./picoclaw.ts so that the
// gateway client is only bundled with the PicoClaw sidebar, not the desktop page.
import { http } from '@/lib/http.ts';
import { picoclawGateway } from '@/lib/picoclaw-gateway.ts';

const sessionIDHeader = 'X-PicoClaw-Session-ID';

export function connectGateway(sessionId?: string) {
  return picoclawGateway.connect({ sessionId });
}

export function releaseRuntimeSession(sessionId?: string) {
  const activeSessionId = sessionId || picoclawGateway.getSessionId();
  if (!activeSessionId) {
    return Promise.resolve(null);
  }

  return http.request({
    method: 'delete',
    url: '/api/picoclaw/runtime/session',
    headers: {
      [sessionIDHeader]: activeSessionId
    }
  });
}

export function sendChatMessage(
  content: string,
  options?: { id?: string; maxSteps?: number; maxRuntimeMs?: number; trackState?: boolean }
) {
  return picoclawGateway.sendChatMessage(content, options);
}

export function sendStopMessage() {
  return picoclawGateway.sendStopMessage();
}

export async function closeGateway() {
  const activeSessionId = picoclawGateway.getSessionId();
  picoclawGateway.close();
  if (!activeSessionId) {
    return;
  }

  await releaseRuntimeSession(activeSessionId).catch(() => undefined);
}

export { picoclawGateway };
