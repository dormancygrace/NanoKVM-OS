import type { NotifyMessage, NotifyTemplate } from '@/api/notify.ts';

// A template as the page edits it; open is its fold.
export type TemplateDraft = NotifyTemplate & { open: boolean };

// The id of a new template: the page chooses it, so a channel can pick the
// template before it is saved.
export const newTemplateId = () => {
  const bytes = crypto.getRandomValues(new Uint8Array(8));
  return 't' + Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
};

// The template as the server takes it: only the messages with a text.
export const templateOf = (draft: TemplateDraft): NotifyTemplate => {
  const messages: Record<string, NotifyMessage> = {};
  for (const [key, m] of Object.entries(draft.messages)) {
    if (m.title.trim() || m.body.trim()) messages[key] = { title: m.title, body: m.body };
  }
  return { id: draft.id, name: draft.name, messages };
};
