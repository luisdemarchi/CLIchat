import type { Bootstrap, ProviderId, Session } from '../types';

declare global {
  interface Window {
    go?: {
      app?: {
        App?: {
          GetBootstrap: () => Promise<Bootstrap>;
          SelectSession: (id: string) => Promise<Session>;
          DeleteSession: (id: string) => Promise<void>;
          ReconnectSession: (id: string) => Promise<void>;
          OpenSessionTerminal: (input: { sessionId: string; providerId?: ProviderId }) => Promise<Session>;
          CreateChat: (input: { providerId: ProviderId; title: string; cwd: string }) => Promise<Session>;
          SendMessage: (input: { sessionId: string; text: string }) => Promise<Session>;
          SendFiles: (input: { sessionId: string; paths: string[] }) => Promise<Session>;
          PickFiles: () => Promise<string[]>;
          RespondToPrompt: (input: { sessionId: string; actionId: string; input: string }) => Promise<Session>;
          SendTerminalInput: (input: { sessionId: string; data: string }) => Promise<void>;
          ResizeTerminal: (input: { sessionId: string; cols: number; rows: number }) => Promise<void>;
          OpenTerminal: (input: { sessionId: string }) => Promise<string>;
          ExternalAttachCommand: (sessionId: string) => Promise<string>;
          FocusTerminal: (sessionId: string) => Promise<void>;
        };
      };
    };
    runtime?: {
      EventsOn: (event: string, callback: (...args: unknown[]) => void) => () => void;
    };
  }
}

const fallback: Bootstrap = {
  providers: [
    {
      id: 'claude',
      name: 'Claude',
      cli: 'claude',
      command: 'claude',
      args: [],
      tag: 'CLAUDE',
      accent: '#6f5adc',
      available: true,
      description: 'Claude Code in an app-managed local session.',
    },
    {
      id: 'gemini',
      name: 'Gemini',
      cli: 'gemini',
      command: 'gemini',
      args: ['--screen-reader'],
      tag: 'GEMINI',
      accent: '#167c80',
      available: true,
      description: 'Gemini CLI in an app-managed local session.',
    },
    {
      id: 'codex',
      name: 'Codex',
      cli: 'codex',
      command: 'codex',
      args: ['--no-alt-screen'],
      tag: 'CODEX',
      accent: '#a45f18',
      available: true,
      description: 'Codex CLI in an app-managed local session.',
    },
  ],
  sessions: [],
  selected: undefined,
  mirror: {
    enabled: false,
    mode: 'preview',
    address: '',
    note: 'Wails backend unavailable in this preview.',
  },
};

function bridge() {
  return window.go?.app?.App;
}

export async function getBootstrap(): Promise<Bootstrap> {
  return bridge()?.GetBootstrap?.() ?? fallback;
}

export async function selectSession(id: string): Promise<Session> {
  const api = bridge();
  if (api?.SelectSession) {
    return api.SelectSession(id);
  }
  return fallback.sessions.find((session) => session.id === id) ?? fallback.sessions[0];
}

export async function deleteSession(id: string): Promise<void> {
  const api = bridge();
  if (api?.DeleteSession) {
    return api.DeleteSession(id);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to close chats.');
}

export async function reconnectSession(id: string): Promise<void> {
  const api = bridge();
  if (api?.ReconnectSession) {
    return api.ReconnectSession(id);
  }
  throw new Error('Wails backend unavailable.');
}

export async function openSessionTerminal(input: { sessionId: string; providerId?: ProviderId }): Promise<Session> {
  const api = bridge();
  if (api?.OpenSessionTerminal) {
    return api.OpenSessionTerminal(input);
  }
  throw new Error('Wails backend unavailable.');
}

export async function createChat(input: { providerId: ProviderId; title: string; cwd: string }): Promise<Session> {
  const api = bridge();
  if (api?.CreateChat) {
    return api.CreateChat(input);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to start real CLIs.');
}

export async function sendMessage(input: { sessionId: string; text: string }): Promise<Session> {
  const api = bridge();
  if (api?.SendMessage) {
    return api.SendMessage(input);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to send messages.');
}

export async function pickFiles(): Promise<string[]> {
  const api = bridge();
  if (api?.PickFiles) {
    return api.PickFiles();
  }
  throw new Error('Wails backend unavailable. Open the desktop app to attach files.');
}

export async function sendFiles(input: { sessionId: string; paths: string[] }): Promise<Session> {
  const api = bridge();
  if (api?.SendFiles) {
    return api.SendFiles(input);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to send files.');
}

export async function respondToPrompt(input: { sessionId: string; actionId: string; input: string }): Promise<Session> {
  const api = bridge();
  if (api?.RespondToPrompt) {
    return api.RespondToPrompt(input);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to respond to the terminal.');
}

export async function sendTerminalInput(input: { sessionId: string; data: string }): Promise<void> {
  const api = bridge();
  if (api?.SendTerminalInput) {
    return api.SendTerminalInput(input);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to control the terminal.');
}

export async function resizeTerminal(input: { sessionId: string; cols: number; rows: number }): Promise<void> {
  const api = bridge();
  if (api?.ResizeTerminal) {
    return api.ResizeTerminal(input);
  }
  // best-effort: silent no-op if bridge missing
}

export async function openTerminal(input: { sessionId: string }): Promise<string> {
  const api = bridge();
  if (api?.OpenTerminal) {
    return api.OpenTerminal(input);
  }
  throw new Error('Wails backend unavailable. Open the desktop app to copy the command.');
}

export async function focusTerminal(sessionId: string): Promise<void> {
  const api = bridge();
  if (api?.FocusTerminal) {
    return api.FocusTerminal(sessionId);
  }
  throw new Error('Wails backend unavailable.');
}

export function onStateUpdate(callback: (payload: Bootstrap) => void): () => void {
  return (
    window.runtime?.EventsOn?.('state:update', (...args: unknown[]) => callback(args[0] as Bootstrap)) ??
    (() => undefined)
  );
}
