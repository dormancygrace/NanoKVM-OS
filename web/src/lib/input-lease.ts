import { client } from '@/lib/websocket.ts';

// Must match router.InputLeaseHeader on the server.
const inputLeaseHeader = 'X-NanoKVM-Input-Lease';

// Headers proving that this tab's WebSocket owns input control. Paste and ATX
// requests from any other tab or viewer are refused while someone controls input.
export function inputLeaseHeaders(): Record<string, string> {
  const lease = client.getInputLease();
  return lease ? { [inputLeaseHeader]: lease } : {};
}
