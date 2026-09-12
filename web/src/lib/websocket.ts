import { ICloseEvent, IMessageEvent, w3cwebsocket as W3cWebSocket } from 'websocket';

import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { getBaseUrl } from '@/lib/service.ts';

export type InputConnectionStatus =
  | 'idle'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'disconnected';

type MessageHandler = (message: IMessageEvent) => void;
type SendData = number[] | ArrayBuffer | Uint8Array;

export enum MessageEvent {
  Heartbeat = 0,
  Keyboard = 1,
  Mouse = 2
}

interface WsClientOptions {
  url?: string;
  heartbeatInterval?: number;
  reconnectInterval?: number;
  maxReconnectAttempts?: number;
}

const DEFAULT_OPTIONS: Required<WsClientOptions> = {
  url: `${getBaseUrl('ws')}/api/ws`,
  heartbeatInterval: 10 * 1000,
  reconnectInterval: 3 * 1000,
  maxReconnectAttempts: Number.POSITIVE_INFINITY
};

export class WsClient {
  private readonly options: Required<WsClientOptions>;
  private instance: W3cWebSocket | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempts = 0;
  private shouldReconnect = true;
  private inputEnabled = false;
  private connectionStatus: InputConnectionStatus = 'idle';
  private readonly statusListeners = new Set<() => void>();
  private lastResponseAt = 0;
  private heartbeatAcknowledged = false;

  public readonly getConnectionStatus = (): InputConnectionStatus => this.connectionStatus;
  public readonly subscribeConnectionStatus = (listener: () => void): (() => void) => {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
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
    if (!enabled && this.inputEnabled) {
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
    }
    this.inputEnabled = enabled;
  }

  public send(data: SendData): boolean {
    const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : data;
    const type = bytes[0];
    if (type === MessageEvent.Keyboard || type === MessageEvent.Mouse) {
      if (!this.inputEnabled) return false;
      this.inputReports.set(type, new Uint8Array(bytes));
    }
    if (!this.instance || !this.isConnected) {
      return false;
    }

    if (data instanceof ArrayBuffer || (data as unknown) instanceof Uint8Array) {
      this.instance.send(data);
    } else {
      this.instance.send(JSON.stringify(data));
    }

    return true;
  }

  public get isConnected(): boolean {
    return this.instance?.readyState === W3cWebSocket.OPEN;
  }

  private createConnection(): void {
    this.cleanup();

    const previous = this.instance;
    this.instance = null;
    previous?.close();
    const socket = new W3cWebSocket(this.options.url);
    this.instance = socket;
    socket.binaryType = 'arraybuffer';

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
    this.reconnectAttempts = 0;
    this.lastResponseAt = Date.now();
    this.heartbeatAcknowledged = false;
    this.updateConnectionStatus('connected');
    this.startHeartbeat();
  }

  private handleClose(event: ICloseEvent): void {
    this.stopHeartbeat();
    this.inputReports.clear();

    if (event.code === 4401) {
      this.shouldReconnect = false;
      this.cleanup();
      this.updateConnectionStatus('disconnected');
      notifyAuthExpired();
      return;
    }

    this.scheduleReconnect();
  }

  private handleError(error: Error): void {
    console.error('[WebSocket] Error:', error);
  }

  private handleMessage(message: IMessageEvent): void {
    try {
      const data = JSON.parse(message.data as string);
      this.lastResponseAt = Date.now();
      if (data.type === 'heartbeat') this.heartbeatAcknowledged = true;
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
      // Older servers do not acknowledge heartbeats. Enable timeout recovery
      // only after the server has demonstrated support for the reply.
      if (
        this.heartbeatAcknowledged &&
        Date.now() - this.lastResponseAt >= 3 * this.options.heartbeatInterval
      ) {
        this.updateConnectionStatus('reconnecting');
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
    this.updateConnectionStatus(this.reconnectAttempts >= 3 ? 'disconnected' : 'reconnecting');
    console.log(`[WebSocket] Reconnecting... (attempt ${this.reconnectAttempts})`);

    this.reconnectTimer = setTimeout(() => {
      this.createConnection();
    }, this.options.reconnectInterval);
  }

  private cleanup(): void {
    this.stopHeartbeat();

    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}

export const client = new WsClient();
