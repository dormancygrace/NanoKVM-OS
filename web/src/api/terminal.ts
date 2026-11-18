import { http } from '@/lib/http.ts';

// A shell on the NanoKVM. It keeps running without a page for a while, so a
// page can attach to it again (server/service/vm/terminal_session.go).
export type TerminalSession = {
  id: string;
  username: string;
  // The command in the foreground; the shell itself at its prompt.
  command: string;
  createdAt: number;
  attached: boolean;
  // Without a page: since when, and when it ends unless one attaches.
  detachedAt?: number;
  endsAt?: number;
  // The page attached now, or last.
  clientIp: string;
  userAgent: string;
  // The signed-in user's own shell: only these can be opened.
  own: boolean;
};

export function getTerminalSessions() {
  return http.get<{ sessions: TerminalSession[]; max: number }>('/api/vm/terminal/sessions');
}

export function closeTerminalSession(id: string) {
  return http.delete<null>(`/api/vm/terminal/sessions/${encodeURIComponent(id)}`);
}
