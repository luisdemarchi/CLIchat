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

type PendingAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Input string `json:"input"`
}

type Session struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	ProviderID        provider.ID     `json:"providerId"`
	ProviderSessionID string          `json:"providerSessionId"`
	ProviderTag       string          `json:"providerTag"`
	ProviderAccent    string          `json:"providerAccent"`
	Status            Status          `json:"status"`
	CWD               string          `json:"cwd,omitempty"`
	AvatarLabel       string          `json:"avatarLabel"`
	LastMessage       string          `json:"lastMessage"`
	CurrentTool       string          `json:"currentTool,omitempty"`
	ProcessID         int             `json:"processId,omitempty"`
	ExternalAttach    string          `json:"externalAttach"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
	Messages          []Message       `json:"messages"`
	TerminalOutput    string          `json:"terminalOutput"`
	TerminalView      string          `json:"terminalView"`
	PendingQuestion   string          `json:"pendingQuestion,omitempty"`
	PendingActions    []PendingAction `json:"pendingActions"`
	TerminalAttached  bool            `json:"terminalAttached"`
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
