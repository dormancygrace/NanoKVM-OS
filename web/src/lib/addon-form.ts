// Pure helpers of the add-on pages that core draws from manifests
// (pages/desktop/menu/settings/addon/, docs/ADDONS.md).

export type AddonText = Record<string, string>;

type FormField = { key: string; type: string };

// The text for the UI language: the language itself ("zh_tw"), its base
// ("zh"), then English, which every manifest has.
export function localized(text: AddonText | undefined, language: string): string {
  if (!text) return '';
  const tag = language.toLowerCase().replace('-', '_');
  return text[tag] ?? text[tag.split('_')[0]] ?? text.en ?? Object.values(text)[0] ?? '';
}

// The settings tab of an add-on's page: "<group>-addon-<id>", so the
// navigation finds its group by the prefix as for the built-in pages.
export function addonTabId(group: string, id: string): string {
  return `${group}-addon-${id}`;
}

// What a form sends: the edited fields only. Status fields are the add-on's
// to report, and an empty password means "keep the current one".
export function submitValues(
  fields: FormField[],
  values: Record<string, unknown>
): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const field of fields) {
    if (field.type === 'status') continue;
    const value = values[field.key];
    if (field.type === 'password' && (value === undefined || value === null || value === '')) {
      continue;
    }
    if (field.type === 'switch') result[field.key] = Boolean(value);
    else if (value !== undefined) result[field.key] = value;
  }
  return result;
}

// The form's starting values from the add-on's data: passwords never come
// back from it and start empty.
export function formValues(
  fields: FormField[],
  data: Record<string, unknown>
): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const field of fields) {
    if (field.type === 'status') continue;
    result[field.key] = field.type === 'password' ? '' : data[field.key];
  }
  return result;
}

// A status value as text: booleans as the given yes/no words, nothing as a dash.
export function statusText(value: unknown, yes: string, no: string): string {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'boolean') return value ? yes : no;
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}
