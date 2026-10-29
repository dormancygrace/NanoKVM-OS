// ATX power control (System > Device) as GET /api/vm/hardware reports it.
// A server from before the switch answers only {version} and always offers
// the power button, so a missing field counts as on.
export function atxFromHardware(data: { atx?: boolean } | null | undefined): boolean {
  return data?.atx !== false;
}

// A server from before the switch has no /api/vm/atx: the setting is not
// shown there rather than as a failure.
export function isMissingEndpoint(error: unknown): boolean {
  return (error as { response?: { status?: number } } | null)?.response?.status === 404;
}
