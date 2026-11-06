import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { ConnectRecovery, RECOVERY_RECONNECT_DELAY } from '@/lib/connect-recovery.ts';
import { HoldKeepalive, KEEPALIVE_MESSAGE } from '@/lib/hold-keepalive.ts';
import { getBaseUrl } from '@/lib/service.ts';

export type InputConnectionStatus =
  'idle' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected';

// The exported MessageEvent enum below shadows the DOM type of the same name.
type SocketMessageEvent = globalThis.MessageEvent;
type MessageHandler = (message: SocketMessageEvent) => void;
type SendData = number[] | ArrayBuffer | Uint8Array;

export enum MessageEvent {
  Heartbeat = 0,
  Keyboard = 1,
  Mouse = 2,
  Control = 3
}

interface WsClientOptions {
  url?: string;
  heartbeatInterval?: number;
  reconnectInterval?: number;
  recoveryReconnectInterval?: number;
  maxReconnectAttempts?: number;
}

const DEFAULT_OPTIONS: Required<WsClientOptions> = {
  url: `${getBaseUrl('ws')}/api/ws`,
  heartbeatInterval: 10 * 1000,
  reconnectInterval: 3 * 1000,
  // Retries soon after a lost connection: see ConnectRecovery.
  recoveryReconnectInterval: RECOVERY_RECONNECT_DELAY,
  maxReconnectAttempts: Number.POSITIVE_INFINITY
};

export class WsClient {
  private readonly options: Required<WsClientOptions>;
  private instance: WebSocket | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private connectTimer: ReturnType<typeof setTimeout> | null = null;
  private readonly recovery = new ConnectRecovery();
  private openedAt: number | null = null;
  private createdAt = 0;
  private reconnectAttempts = 0;
  private reconnectingSince = 0;
  private shouldReconnect = true;
  private captureInputEnabled = false;
  private controlEnabled = false;
  private inputLease = '';
  private effectiveInputEnabled = false;
  private connectionStatus: InputConnectionStatus = 'idle';
  private readonly statusListeners = new Set<() => void>();
  private readonly controlListeners = new Set<() => void>();
  private lastHeartbeatAt = 0;
  private missedHeartbeats = 0;
  private heartbeatAcknowledged = false;
  private readonly holdKeepalive = new HoldKeepalive(() => {
    this.send(new Uint8Array([KEEPALIVE_MESSAGE]));
  });

  public readonly getConnectionStatus = (): InputConnectionStatus => this.connectionStatus;
  public readonly subscribeConnectionStatus = (listener: () => void): (() => void) => {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  };
  public readonly getControlEnabled = (): boolean => this.controlEnabled;
  // Proves to HTTP input routes (paste, ATX) that this tab owns input control.
  public readonly getInputLease = (): string => this.inputLease;
  public readonly subscribeControlStatus = (listener: () => void): (() => void) => {
    this.controlListeners.add(listener);
    return () => this.controlListeners.delete(listener);
  };

  private updateConnectionStatus(status: InputConnectionStatus): void {
    if (this.connectionStatus === status) return;
    this.connectionStatus = status;
    this.statusListeners.forEach((listener) => listener());
  }
  private readonly inputReports = new Map<number, Uint8Array>();

  private readonly eventHandlers = new Map<string, Set<MessageHandler>>();

  constructor(options: WsClientOptions = {}) {
    this.options = { ...DEFAULT_OPTIONS, ...options };
    // A browser may cut the sockets of a background tab and tell the page
    // only when it comes back (Safari, energy saving); the open of a socket
    // made meanwhile may never arrive either. On return the retries start
    // afresh: the first goes at once, whatever happened in the background.
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState !== 'visible') return;
        this.reconnectAttempts = 0;
        this.retryAtOnce = true;
        this.reconnectNow();
      });
    }
  }

  private retryAtOnce = true;

  private static pageVisible(): boolean {
    return typeof document === 'undefined' || document.visibilityState === 'visible';
  }

  private reconnectNow(): void {
    if (!this.shouldReconnect) return;
    // The visible retry budget is fresh, including a handshake already in flight.
    if (
      this.connectionStatus === 'disconnected' &&
      (this.reconnectTimer !== null || this.instance?.readyState === WebSocket.CONNECTING)
    ) {
      this.updateConnectionStatus('reconnecting');
    }
    if (!this.reconnectTimer) return;
    this.retryAtOnce = false;
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.createConnection();
  }

  public connect(): void {
    this.shouldReconnect = true;
    this.reconnectAttempts = 0;
    this.updateConnectionStatus('connecting');
    this.createConnection();
  }

  public close(): void {
    this.shouldReconnect = false;
    this.cleanup();

    const previous = this.instance;
    this.instance = null;
    previous?.close();
    this.inputReports.clear();
    // The server drops ownership with the socket; do not keep claiming it.
    this.setControlEnabled(false);
    this.updateConnectionStatus('idle');
  }

  public on(type: string, handler: MessageHandler): () => void {
    if (!this.eventHandlers.has(type)) {
      this.eventHandlers.set(type, new Set());
    }

    this.eventHandlers.get(type)!.add(handler);

    return () => {
      const handlers = this.eventHandlers.get(type);
      if (handlers) {
        handlers.delete(handler);
        if (handlers.size === 0) {
          this.eventHandlers.delete(type);
        }
      }
    };
  }

  public off(type: string, handler?: MessageHandler): void {
    if (handler) {
      const handlers = this.eventHandlers.get(type);
      if (handlers) {
        handlers.delete(handler);
        if (handlers.size === 0) {
          this.eventHandlers.delete(type);
        }
      }
    } else {
      this.eventHandlers.delete(type);
    }
  }

  public setInputEnabled(enabled: boolean): void {
    this.captureInputEnabled = enabled;
    this.updateInputGate();
  }

  public requestControl(enabled: boolean): boolean {
    if (!this.instance || !this.isConnected) return false;
    this.instance.send(new Uint8Array([MessageEvent.Control, enabled ? 1 : 0]));
    return true;
  }

  private setControlEnabled(enabled: boolean): void {
    if (!enabled) this.inputLease = '';
    if (this.controlEnabled === enabled) return;
    this.controlEnabled = enabled;
    this.updateInputGate();
    this.controlListeners.forEach((listener) => listener());
  }

  private updateInputGate(): void {
    const enabled = this.captureInputEnabled && this.controlEnabled;
    if (!enabled && this.effectiveInputEnabled) {
      for (const [type, last] of this.inputReports) {
        const release = new Uint8Array(last);
        if (type === MessageEvent.Keyboard) release.fill(0, 1);
        else {
          release[1] = 0;
          // Relative movement must be zero; absolute coordinates stay put.
          if (release.length === 5 || release.length === 6) release.fill(0, 2);
          else release.fill(0, 6);
        }
        this.send(release);
      }
      this.inputReports.clear();
      this.holdKeepalive.reset();
    }
    this.effectiveInputEnabled = enabled;
  }

  public send(data: SendData): boolean {
    const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : data;
    const type = bytes[0];
    if (type === MessageEvent.Keyboard || type === MessageEvent.Mouse) {
      if (!this.effectiveInputEnabled) return false;
      this.inputReports.set(type, new Uint8Array(bytes));
    }
    if (!this.instance || !this.isConnected) {
      return false;
    }

    if (data instanceof ArrayBuffer || (data as unknown) instanceof Uint8Array) {
      this.instance.send(data as BufferSource);
    } else {
      this.instance.send(JSON.stringify(data));
    }

    this.holdKeepalive.observe(bytes);
    return true;
  }

  public get isConnected(): boolean {
    return this.instance?.readyState === WebSocket.OPEN;
  }

  private createConnection(): void {
    this.cleanup();

    const previous = this.instance;
    this.instance = null;
    previous?.close();
    const socket = new WebSocket(this.options.url);
    this.instance = socket;
    this.openedAt = null;
    this.createdAt = Date.now();
    socket.binaryType = 'arraybuffer';
    const timeout = this.recovery.connectTimeout(this.createdAt);
    if (timeout !== null) {
      this.connectTimer = setTimeout(() => {
        this.connectTimer = null;
        if (socket.readyState === WebSocket.CONNECTING) socket.close();
      }, timeout);
    }

    // A late callback from a replaced connection must not affect the new one.
    socket.onopen = () => {
      if (this.instance === socket) this.handleOpen();
    };
    socket.onclose = (event) => {
      if (this.instance === socket) this.handleClose(event);
    };
    socket.onerror = (event) => {
      if (this.instance === socket) this.handleError(event);
    };
    socket.onmessage = (event) => {
      if (this.instance === socket) this.handleMessage(event);
    };
  }

  private handleOpen(): void {
    this.openedAt = Date.now();
    this.clearConnectTimer();
    this.recovery.opened(this.openedAt - this.createdAt);
    this.reconnectAttempts = 0;
    this.lastHeartbeatAt = Date.now();
    this.missedHeartbeats = 0;
    this.heartbeatAcknowledged = false;
    this.send(new Uint8Array([KEEPALIVE_MESSAGE]));
    this.updateConnectionStatus('connected');
    this.startHeartbeat();
  }

  private handleClose(event: CloseEvent): void {
    this.clearConnectTimer();
    if (this.openedAt !== null) this.recovery.lost(Date.now());
    this.stopHeartbeat();
    this.inputReports.clear();
    this.setControlEnabled(false);

    if (event.code === 4401) {
      this.shouldReconnect = false;
      this.cleanup();
      this.updateConnectionStatus('disconnected');
      notifyAuthExpired();
      return;
    }

    this.scheduleReconnect();
  }

  private handleError(error: Event): void {
    console.error('[WebSocket] Error:', error);
  }

  private handleMessage(message: SocketMessageEvent): void {
    try {
      const data = JSON.parse(message.data as string);
      this.missedHeartbeats = 0;
      if (data.type === 'heartbeat') this.heartbeatAcknowledged = true;
      if (data.type === 'control') {
        const status = JSON.parse(data.data as string) as { enabled?: unknown; lease?: unknown };
        const enabled = status.enabled === true;
        // The server sends this socket's lease only while it owns input.
        this.inputLease = enabled && typeof status.lease === 'string' ? status.lease : '';
        this.setControlEnabled(enabled);
      }
      const handlers = this.eventHandlers.get(data.type);

      if (handlers) {
        handlers.forEach((handler) => handler(message));
      }
    } catch (err) {
      console.log(err);
    }
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.send(new Uint8Array([MessageEvent.Heartbeat]));
    this.heartbeatTimer = setInterval(() => {
      const now = Date.now();
      // A suspended page did not send its scheduled probes. Give the open
      // socket one fresh heartbeat window on return instead of closing it for
      // time spent with paused or throttled browser timers; a socket that
      // died meanwhile (a new network after sleep) never reports a close.
      if (now - this.lastHeartbeatAt >= 3 * this.options.heartbeatInterval) {
        this.missedHeartbeats = 1;
      }
      this.lastHeartbeatAt = now;
      // Count unanswered windows that actually ran. Older servers do not
      // acknowledge heartbeats, so keep their existing behavior.
      if (this.heartbeatAcknowledged && ++this.missedHeartbeats >= 3) {
        this.updateConnectionStatus('reconnecting');
        this.recovery.lost(now);
        const previous = this.instance;
        this.instance = null;
        previous?.close();
        this.stopHeartbeat();
        this.inputReports.clear();
        this.scheduleReconnect();
        return;
      }
      this.send(new Uint8Array([MessageEvent.Heartbeat]));
    }, this.options.heartbeatInterval);
  }

  private stopHeartbeat(): void {
    this.holdKeepalive.reset();
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private scheduleReconnect(): void {
    if (!this.shouldReconnect) {
      return;
    }

    if (this.reconnectAttempts >= this.options.maxReconnectAttempts) {
      this.updateConnectionStatus('disconnected');
      console.error('[WebSocket] Max reconnect attempts reached');
      return;
    }

    this.reconnectAttempts++;
    // Disconnected at the first retry 6 s or more after the loss, whatever the retry delays: the
    // stock backoff showed it once the second retry failed, about 3 s when retries
    // fail at once and much later while they hang in SYN backoff.
    const now = Date.now();
    if (this.reconnectAttempts === 1) this.reconnectingSince = now;
    this.updateConnectionStatus(
      now - this.reconnectingSince >= 2 * this.options.reconnectInterval
        ? 'disconnected'
        : 'reconnecting'
    );
    console.log(`[WebSocket] Reconnecting... (attempt ${this.reconnectAttempts})`);

    // The first retry of a visible page, and the first after it comes back,
    // goes at once: a lost connection is usually back within a handshake.
    // Later retries soon after a lost connection come soon too (see
    // ConnectRecovery). A hidden page keeps the backoff.
    const visible = WsClient.pageVisible();
    const atOnce = visible && (this.reconnectAttempts === 1 || this.retryAtOnce);
    this.retryAtOnce = false;
    let delay = this.options.reconnectInterval;
    if (atOnce) delay = 0;
    else if (visible && this.recovery.recovering(Date.now()))
      delay = this.options.recoveryReconnectInterval;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.createConnection();
    }, delay);
  }

  private cleanup(): void {
    this.stopHeartbeat();

    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    this.clearConnectTimer();
  }

  private clearConnectTimer(): void {
    if (this.connectTimer) {
      clearTimeout(this.connectTimer);
      this.connectTimer = null;
    }
  }
}

export const client = new WsClient();
