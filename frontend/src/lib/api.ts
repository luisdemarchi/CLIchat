import type { Bootstrap, ProviderId, Session } from '../types';

declare global {
  interface Window {
    go?: {
      app?: {
        App?: {
          GetBootstrap: () => Promise<Bootstrap>;
          SelectSession: (id: string) => Promise<Session>;
          CreateChat: (input: { providerId: ProviderId; title: string; cwd: string }) => Promise<Session>;
          SendMessage: (input: { sessionId: string; text: string }) => Promise<Session>;
          RespondToPrompt: (input: { sessionId: string; actionId: string; input: string }) => Promise<Session>;
          SendTerminalInput: (input: { sessionId: string; data: string }) => Promise<void>;
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
      description: 'Claude Code em uma sessao local controlada pelo app.',
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
      description: 'Gemini CLI em uma sessao local controlada pelo app.',
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
      description: 'Codex CLI em uma sessao local controlada pelo app.',
    },
  ],
  sessions: [],
  selected: undefined,
  mirror: {
    enabled: false,
    mode: 'preview',
    address: '',
    note: 'Backend Wails indisponivel neste preview.',
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

export async function createChat(input: { providerId: ProviderId; title: string; cwd: string }): Promise<Session> {
  const api = bridge();
  if (api?.CreateChat) {
    return api.CreateChat(input);
  }
  throw new Error('Backend Wails indisponivel. Abra pelo app desktop para iniciar CLIs reais.');
}

export async function sendMessage(input: { sessionId: string; text: string }): Promise<Session> {
  const api = bridge();
  if (api?.SendMessage) {
    return api.SendMessage(input);
  }
  throw new Error('Backend Wails indisponivel. Abra pelo app desktop para enviar mensagens.');
}

export async function respondToPrompt(input: { sessionId: string; actionId: string; input: string }): Promise<Session> {
  const api = bridge();
  if (api?.RespondToPrompt) {
    return api.RespondToPrompt(input);
  }
  throw new Error('Backend Wails indisponivel. Abra pelo app desktop para responder ao terminal.');
}

export async function sendTerminalInput(input: { sessionId: string; data: string }): Promise<void> {
  const api = bridge();
  if (api?.SendTerminalInput) {
    return api.SendTerminalInput(input);
  }
  throw new Error('Backend Wails indisponivel. Abra pelo app desktop para controlar o terminal.');
}

export async function openTerminal(input: { sessionId: string }): Promise<string> {
  const api = bridge();
  if (api?.OpenTerminal) {
    return api.OpenTerminal(input);
  }
  throw new Error('Backend Wails indisponivel. Abra pelo app desktop para copiar o comando.');
}

export async function focusTerminal(sessionId: string): Promise<void> {
  const api = bridge();
  if (api?.FocusTerminal) {
    return api.FocusTerminal(sessionId);
  }
  throw new Error('Backend Wails indisponivel.');
}

export function onStateUpdate(callback: (payload: Bootstrap) => void): () => void {
  return (
    window.runtime?.EventsOn?.('state:update', (...args: unknown[]) => callback(args[0] as Bootstrap)) ??
    (() => undefined)
  );
}
