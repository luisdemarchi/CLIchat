import { Check, Circle, MonitorUp, Network, Plus, Send, TerminalSquare } from 'lucide-react';
import { FormEvent, useEffect, useMemo, useState } from 'react';
import { createChat, getBootstrap, onStateUpdate, selectSession, sendMessage } from './lib/api';
import type { Bootstrap, ProviderId, Session } from './types';

function statusLabel(status: Session['status']) {
  switch (status) {
    case 'busy':
      return 'trabalhando';
    case 'waiting':
      return 'aguardando';
    case 'offline':
      return 'offline';
    default:
      return 'online';
  }
}

export function App() {
  const [state, setState] = useState<Bootstrap | null>(null);
  const [selectedID, setSelectedID] = useState('');
  const [draft, setDraft] = useState('');
  const [newProvider, setNewProvider] = useState<ProviderId>('claude');
  const [error, setError] = useState('');

  useEffect(() => {
    getBootstrap().then((payload) => {
      setState(payload);
      setSelectedID(payload.selected?.id ?? payload.sessions[0]?.id ?? '');
    });
    return onStateUpdate((payload) => {
      setState(payload);
      setSelectedID((current) => current || payload.selected?.id || payload.sessions[0]?.id || '');
    });
  }, []);

  const selected = useMemo(() => {
    if (!state) return undefined;
    return state.sessions.find((session) => session.id === selectedID) ?? state.selected ?? state.sessions[0];
  }, [selectedID, state]);

  async function handleSelect(session: Session) {
    setSelectedID(session.id);
    const next = await selectSession(session.id);
    setState((current) => current && { ...current, selected: next });
  }

  async function handleNewChat() {
    if (!state) return;
    setError('');
    const provider = state.providers.find((item) => item.id === newProvider) ?? state.providers[0];
    try {
      const created = await createChat({
        providerId: provider.id,
        title: `${provider.name} ${state.sessions.length + 1}`,
        cwd: '',
      });
      setSelectedID(created.id);
      setState((current) =>
        current && {
          ...current,
          sessions: [created, ...current.sessions.filter((session) => session.id !== created.id)],
          selected: created,
        },
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!selected || draft.trim() === '') return;

    const text = draft.trim();
    setDraft('');
    setError('');
    try {
      const updated = await sendMessage({ sessionId: selected.id, text });
      setState((current) => {
        if (!current) return current;
        return {
          ...current,
          sessions: current.sessions.map((session) => (session.id === updated.id ? updated : session)),
          selected: updated,
        };
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  if (!state) {
    return <main className="shell loading">Carregando</main>;
  }

  return (
    <main className="shell">
      <aside className="sidebar">
        <header className="profile">
          <div className="profile-photo">LC</div>
          <div>
            <h1>Agent Chat Local</h1>
            <p>{state.mirror.note}</p>
          </div>
          <span className="network-pill" title="Espelhamento local/Tailscale">
            <Network size={16} />
          </span>
        </header>

        <section className="new-chat">
          <select value={newProvider} onChange={(event) => setNewProvider(event.target.value as ProviderId)}>
            {state.providers.map((provider) => (
              <option key={provider.id} value={provider.id}>
                {provider.name}
              </option>
            ))}
          </select>
          <button type="button" onClick={handleNewChat} title="Novo chat">
            <Plus size={18} />
          </button>
        </section>
        {error ? <div className="error-banner">{error}</div> : null}

        <section className="status-strip" aria-label="Status">
          {state.providers.map((provider) => (
            <div className="status-photo" key={provider.id} style={{ borderColor: provider.accent }}>
              <span>{provider.tag.slice(0, 2)}</span>
              <small>{provider.available ? <Check size={12} /> : <Circle size={12} />}</small>
            </div>
          ))}
        </section>

        <nav className="chat-list" aria-label="Conversas">
          {state.sessions.map((session) => (
            <button
              className={`chat-row ${session.id === selected?.id ? 'active' : ''}`}
              key={session.id}
              type="button"
              onClick={() => void handleSelect(session)}
            >
              <span className="avatar" style={{ background: session.providerAccent }}>
                {session.avatarLabel}
              </span>
              <span className="chat-copy">
                <strong>{session.title}</strong>
                <span>{session.lastMessage}</span>
              </span>
              <span className={`presence ${session.status}`}>{statusLabel(session.status)}</span>
            </button>
          ))}
        </nav>
      </aside>

      {selected ? (
        <section className="conversation">
          <header className="conversation-header">
            <div className="avatar large" style={{ background: selected.providerAccent }}>
              {selected.avatarLabel}
            </div>
            <div className="title-block">
              <strong>{selected.title}</strong>
            <span>
              {statusLabel(selected.status)}
              {selected.processId ? ` - pid ${selected.processId}` : ''}
              {selected.currentTool ? ` - ${selected.currentTool}` : ''}
            </span>
            {!selected.terminalAttached ? <code className="attach-command">{selected.externalAttach}</code> : null}
          </div>
            <span className="provider-tag" style={{ borderColor: selected.providerAccent, color: selected.providerAccent }}>
              {selected.providerTag}
            </span>
            <button type="button" className="icon-button" title={selected.externalAttach}>
              <MonitorUp size={18} />
            </button>
          </header>

          <div className="messages">
            {(selected.messages ?? []).map((message) => (
              <article className={`bubble ${message.role}`} key={message.id}>
                <p>{message.text}</p>
                <time>{new Date(message.createdAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</time>
              </article>
            ))}
          </div>

          <form className="composer" onSubmit={handleSubmit}>
            <span className="terminal-indicator" title={selected.externalAttach}>
              <TerminalSquare size={18} />
            </span>
            <input
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              placeholder={`Mensagem para ${selected.providerTag}`}
            />
            <button type="submit" title="Enviar">
              <Send size={18} />
            </button>
          </form>
        </section>
      ) : (
        <section className="empty-conversation">
          <div>
            <TerminalSquare size={34} />
            <strong>Escolha um provedor e inicie um chat.</strong>
            <span>O app vai abrir o CLI real em um terminal oculto e mostrar a saida aqui.</span>
          </div>
        </section>
      )}
    </main>
  );
}
