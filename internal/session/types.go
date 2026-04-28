package session

import "agent-chat-local/internal/provider"

type Status string

const (
	Idle    Status = "idle"
	Busy    Status = "busy"
	Waiting Status = "waiting"
	Offline Status = "offline"
)

type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
	System    Role = "system"
)

type MessageType string

const (
	Text  MessageType = "text"
	Audio MessageType = "audio"
	Image MessageType = "image"
)

type Message struct {
	ID        string      `json:"id"`
	Role      Role        `json:"role"`
	Type      MessageType `json:"type"`
	Text      string      `json:"text"`
	CreatedAt string      `json:"createdAt"`
}

type Session struct {
	ID               string      `json:"id"`
	Title            string      `json:"title"`
	ProviderID       provider.ID `json:"providerId"`
	ProviderTag      string      `json:"providerTag"`
	ProviderAccent   string      `json:"providerAccent"`
	Status           Status      `json:"status"`
	CWD              string      `json:"cwd,omitempty"`
	AvatarLabel      string      `json:"avatarLabel"`
	LastMessage      string      `json:"lastMessage"`
	CurrentTool      string      `json:"currentTool,omitempty"`
	ProcessID        int         `json:"processId,omitempty"`
	ExternalAttach   string      `json:"externalAttach"`
	CreatedAt        string      `json:"createdAt"`
	UpdatedAt        string      `json:"updatedAt"`
	Messages         []Message   `json:"messages"`
	PendingQuestion  string      `json:"pendingQuestion,omitempty"`
	TerminalAttached bool        `json:"terminalAttached"`
}

type CreateInput struct {
	ProviderID provider.ID `json:"providerId"`
	Title      string      `json:"title"`
	CWD        string      `json:"cwd"`
}

type SendInput struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}
