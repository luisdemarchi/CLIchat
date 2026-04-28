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
          ExternalAttachCommand: (sessionId: string) => Promise<string>;
        };
      };
    };
    runtime?: {
      EventsOn: (event: string, callback: (payload: Bootstrap) => void) => () => void;
    };
  }
}

const fallback: Bootstrap = {
  providers: [
    {
      id: 'claude',
      name: 'Claude',
      cli: 'claude',
      tag: 'CLAUDE',
      accent: '#6f5adc',
      available: true,
      description: 'Claude Code em uma sessao local controlada pelo app.',
    },
    {
      id: 'gemini',
      name: 'Gemini',
      cli: 'gemini',
      tag: 'GEMINI',
      accent: '#167c80',
      available: true,
      description: 'Gemini CLI em uma sessao local controlada pelo app.',
    },
    {
      id: 'codex',
      name: 'Codex',
      cli: 'codex',
      tag: 'CODEX',
      accent: '#a45f18',
      available: true,
      description: 'Codex CLI em uma sessao local controlada pelo app.',
    },
  ],
  sessions: [
    {
      id: 'demo-claude',
      title: 'Claude local',
      providerId: 'claude',
      providerTag: 'CLAUDE',
      providerAccent: '#6f5adc',
      status: 'idle',
      avatarLabel: 'CL',
      lastMessage: 'Sessao pronta para conectar ao CLI claude.',
      externalAttach: 'agentctl attach demo-claude',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      terminalAttached: false,
      messages: [
        {
          id: 'demo-message',
          role: 'system',
          text: 'Preview web. No app desktop, estes dados vem do backend Go.',
          createdAt: new Date().toISOString(),
        },
      ],
    },
  ],
  selected: undefined,
  mirror: {
    enabled: false,
    mode: 'preview',
    address: '',
    note: 'Preview web sem backend Wails.',
  },
};
fallback.selected = fallback.sessions[0];

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
  const provider = fallback.providers.find((item) => item.id === input.providerId) ?? fallback.providers[0];
  const session: Session = {
    id: `preview-${Date.now()}`,
    title: input.title || `Novo chat ${provider.name}`,
    providerId: provider.id,
    providerTag: provider.tag,
    providerAccent: provider.accent,
    status: 'idle',
    cwd: input.cwd,
    avatarLabel: provider.name.slice(0, 2).toUpperCase(),
    lastMessage: 'Chat criado no preview.',
    externalAttach: 'agentctl attach preview',
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    terminalAttached: false,
    messages: [],
  };
  fallback.sessions = [session, ...fallback.sessions];
  fallback.selected = session;
  return session;
}

export async function sendMessage(input: { sessionId: string; text: string }): Promise<Session> {
  const api = bridge();
  if (api?.SendMessage) {
    return api.SendMessage(input);
  }
  const session = fallback.sessions.find((item) => item.id === input.sessionId) ?? fallback.sessions[0];
  session.messages = [
    ...session.messages,
    { id: `user-${Date.now()}`, role: 'user', text: input.text, createdAt: new Date().toISOString() },
  ];
  session.lastMessage = input.text;
  session.updatedAt = new Date().toISOString();
  fallback.selected = session;
  return session;
}

export function onStateUpdate(callback: (payload: Bootstrap) => void): () => void {
  return window.runtime?.EventsOn?.('state:update', callback) ?? (() => undefined);
}
