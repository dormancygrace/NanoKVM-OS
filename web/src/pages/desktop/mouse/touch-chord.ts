// Keep touch identifiers: TouchList ordering can change as fingers lift.
type Contact = { identifier: number; clientX: number; clientY: number };
const SLOP = 8;
const TAP_MS = 350;

export function createTouchChord() {
  let contacts: Contact[] = [];
  let started = 0;
  let tap = false;
  let last = { x: 0, y: 0 };
  const center = (points: Contact[]) => ({
    x: (points[0].clientX + points[1].clientX) / 2,
    y: (points[0].clientY + points[1].clientY) / 2
  });
  function reset() {
    contacts = [];
    tap = false;
  }
  function track(points: Contact[]) {
    for (const original of contacts) {
      const point = points.find((p) => p.identifier === original.identifier);
      if (
        point &&
        Math.hypot(point.clientX - original.clientX, point.clientY - original.clientY) > SLOP
      ) {
        tap = false;
      }
    }
  }
  return {
    reset,
    start(points: Contact[], now: number, allowTap: boolean) {
      reset();
      if (points.length !== 2) return;
      contacts = points.map((p) => ({
        identifier: p.identifier,
        clientX: p.clientX,
        clientY: p.clientY
      }));
      started = now;
      tap = allowTap;
      last = center(contacts);
    },
    move(points: Contact[]) {
      if (
        contacts.length !== 2 ||
        points.length !== 2 ||
        contacts.some((p) => !points.some((q) => q.identifier === p.identifier))
      ) {
        reset();
        return null;
      }
      track(points);
      const current = center(points);
      const dx = current.x - last.x;
      const dy = current.y - last.y;
      // Ignore finger jitter and stationary touchmove events.
      if (Math.max(Math.abs(dx), Math.abs(dy)) < SLOP) return null;
      tap = false;
      last = current;
      return {
        x: Math.abs(dx) >= SLOP ? Math.sign(dx) : 0,
        y: Math.abs(dy) >= SLOP ? Math.sign(dy) : 0
      };
    },
    end(remaining: Contact[], changed: Contact[], now: number) {
      track([...remaining, ...changed]);
      const secondary = contacts[1];
      const primary = contacts[0];
      const clicked =
        tap &&
        now - started <= TAP_MS &&
        !!secondary &&
        changed.some((p) => p.identifier === secondary.identifier) &&
        (remaining.some((p) => p.identifier === primary.identifier) || remaining.length === 0);
      reset();
      return clicked;
    }
  };
}
