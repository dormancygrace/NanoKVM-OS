type Operation = { state: string; id?: string };

// Keep this tracker for the lifetime of the loaded interface, across settings
// remounts. A full page reload starts a fresh tracker and acknowledges success.
export class UpdateNoticeTracker {
  private pending = new Set<string>();

  expect(id?: string) {
    if (id) this.pending.add(id);
  }

  forget(id?: string) {
    if (id) this.pending.delete(id);
  }

  observe(operation: Operation) {
    if (operation.state === 'installing') this.expect(operation.id);
  }

  visible(operation: Operation) {
    return (
      operation.state !== 'installed' || Boolean(operation.id && this.pending.has(operation.id))
    );
  }
}
