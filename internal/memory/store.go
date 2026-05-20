package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/luisdemarchi/CLIchat/internal/agent"
)

type Store struct {
	path   string
	sqlite string
	mu     sync.Mutex
}

type ConversationMemory struct {
	ConversationID string `json:"conversationId"`
	ProviderID     string `json:"providerId"`
	Title          string `json:"title"`
	Topic          string `json:"topic"`
	Summary        string `json:"summary"`
	MessageCount   int    `json:"messageCount"`
	UpdatedAt      string `json:"updatedAt"`
}

type SearchResult struct {
	MessageID      string `json:"messageId"`
	ConversationID string `json:"conversationId"`
	ProviderID     string `json:"providerId"`
	Title          string `json:"title"`
	Topic          string `json:"topic"`
	Role           string `json:"role"`
	Text           string `json:"text"`
	Snippet        string `json:"snippet"`
	CreatedAt      string `json:"createdAt"`
}

func New(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("memory path is required")
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, errors.New("sqlite3 not found; install sqlite3 to enable CLIchat memory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &Store{path: path, sqlite: sqlite}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) migrate() error {
	_, err := s.run(`
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  provider_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  topic TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  cwd TEXT NOT NULL DEFAULT '',
  origin TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  role TEXT NOT NULL,
  text TEXT NOT NULL,
  source_id TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_source
  ON messages(source_id)
  WHERE source_id IS NOT NULL AND source_id != '';
CREATE INDEX IF NOT EXISTS idx_messages_conversation_created
  ON messages(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS conversation_topics (
  conversation_id TEXT NOT NULL,
  topic TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(conversation_id, topic),
  FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
  conversation_id UNINDEXED,
  role UNINDEXED,
  text,
  content='messages',
  content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, conversation_id, role, text)
  VALUES (new.rowid, new.conversation_id, new.role, new.text);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, conversation_id, role, text)
  VALUES ('delete', old.rowid, old.conversation_id, old.role, old.text);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, conversation_id, role, text)
  VALUES ('delete', old.rowid, old.conversation_id, old.role, old.text);
  INSERT INTO messages_fts(rowid, conversation_id, role, text)
  VALUES (new.rowid, new.conversation_id, new.role, new.text);
END;
`)
	return err
}

func (s *Store) SyncSnapshot(instances []agent.Instance) error {
	for _, inst := range instances {
		if err := s.SyncInstance(inst); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SyncInstance(inst agent.Instance) error {
	if strings.TrimSpace(inst.ID) == "" {
		return nil
	}
	mem := BuildConversationMemory(inst, 12000)
	var b strings.Builder
	b.WriteString("BEGIN;\n")
	b.WriteString("INSERT INTO conversations(id, provider_id, title, topic, summary, cwd, origin, created_at, updated_at) VALUES(")
	b.WriteString(joinSQL(
		sqlString(mem.ConversationID),
		sqlString(mem.ProviderID),
		sqlString(mem.Title),
		sqlString(mem.Topic),
		sqlString(mem.Summary),
		sqlString(inst.CWD),
		sqlString(string(inst.Origin)),
		sqlString(inst.CreatedAt),
		sqlString(inst.UpdatedAt),
	))
	b.WriteString(") ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id, title=excluded.title, topic=excluded.topic, summary=excluded.summary, cwd=excluded.cwd, origin=excluded.origin, updated_at=excluded.updated_at;\n")
	if mem.Topic != "" {
		b.WriteString("INSERT OR IGNORE INTO conversation_topics(conversation_id, topic, created_at) VALUES(")
		b.WriteString(joinSQL(sqlString(mem.ConversationID), sqlString(mem.Topic), sqlString(inst.UpdatedAt)))
		b.WriteString(");\n")
	}
	for _, msg := range inst.Messages {
		if strings.TrimSpace(msg.ID) == "" || strings.TrimSpace(msg.Text) == "" {
			continue
		}
		b.WriteString("INSERT OR IGNORE INTO messages(id, conversation_id, role, text, source_id, created_at) VALUES(")
		b.WriteString(joinSQL(
			sqlString(msg.ID),
			sqlString(inst.ID),
			sqlString(string(msg.Role)),
			sqlString(msg.Text),
			sqlNullable(msg.SourceID),
			sqlString(msg.CreatedAt),
		))
		b.WriteString(");\n")
	}
	b.WriteString("COMMIT;\n")
	_, err := s.run(b.String())
	return err
}

func (s *Store) DeleteConversation(conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	_, err := s.run("PRAGMA foreign_keys=ON;\nDELETE FROM conversations WHERE id=" + sqlString(conversationID) + ";\n")
	return err
}

func (s *Store) Conversation(conversationID string) (ConversationMemory, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationMemory{}, errors.New("conversation id is required")
	}
	var rows []ConversationMemory
	err := s.queryJSON(`
SELECT
  c.id AS conversationId,
  c.provider_id AS providerId,
  c.title AS title,
  c.topic AS topic,
  c.summary AS summary,
  c.updated_at AS updatedAt,
  COUNT(m.id) AS messageCount
FROM conversations c
LEFT JOIN messages m ON m.conversation_id = c.id
WHERE c.id = `+sqlString(conversationID)+`
GROUP BY c.id
LIMIT 1;
`, &rows)
	if err != nil {
		return ConversationMemory{}, err
	}
	if len(rows) == 0 {
		return ConversationMemory{}, errors.New("conversation memory not found")
	}
	return rows[0], nil
}

func (s *Store) SearchConversation(conversationID string, query string, limit int) ([]SearchResult, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, errors.New("conversation id is required")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	fts := ftsQuery(query)
	if fts == "" {
		return nil, errors.New("query is required")
	}
	var rows []SearchResult
	err := s.queryJSON(`
SELECT
  m.id AS messageId,
  m.conversation_id AS conversationId,
  c.provider_id AS providerId,
  c.title AS title,
  c.topic AS topic,
  m.role AS role,
  m.text AS text,
  snippet(messages_fts, 2, '', '', '...', 18) AS snippet,
  m.created_at AS createdAt
FROM messages_fts
JOIN messages m ON m.rowid = messages_fts.rowid
JOIN conversations c ON c.id = m.conversation_id
WHERE messages_fts MATCH `+sqlString(fts)+`
  AND m.conversation_id = `+sqlString(conversationID)+`
ORDER BY bm25(messages_fts)
LIMIT `+strconv.Itoa(limit)+`;
`, &rows)
	return rows, err
}

func BuildConversationMemory(inst agent.Instance, maxChars int) ConversationMemory {
	if maxChars <= 0 {
		maxChars = 12000
	}
	topic := agent.SmartTopic(inst.Messages, firstNonEmpty(inst.Topic, inst.Title))
	title := topic
	if title == "" {
		title = firstNonEmpty(inst.Title, inst.ProviderID)
	}
	return ConversationMemory{
		ConversationID: inst.ID,
		ProviderID:     inst.ProviderID,
		Title:          title,
		Topic:          topic,
		Summary:        summarize(inst.Messages, maxChars),
		MessageCount:   len(inst.Messages),
		UpdatedAt:      inst.UpdatedAt,
	}
}

func summarize(messages []agent.Message, maxChars int) string {
	lines := make([]string, 0, len(messages))
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == agent.RoleSystem {
			continue
		}
		text := compact(msg.Text, 900)
		if text == "" {
			continue
		}
		line := "- " + roleLabel(msg.Role) + ": " + text
		if total+len(line)+1 > maxChars {
			break
		}
		lines = append([]string{line}, lines...)
		total += len(line) + 1
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func compact(text string, max int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return ""
	}
	if max > 0 && len([]rune(text)) > max {
		runes := []rune(text)
		text = string(runes[:max-3]) + "..."
	}
	return text
}

func roleLabel(role agent.Role) string {
	switch role {
	case agent.RoleUser:
		return "User"
	case agent.RoleAssistant:
		return "Assistant"
	default:
		return "System"
	}
}

func (s *Store) queryJSON(sql string, dest any) error {
	out, err := s.run(".mode json\n" + sql)
	if err != nil {
		return err
	}
	data := bytes.TrimSpace(out)
	if len(data) == 0 {
		data = []byte("[]")
	}
	return json.Unmarshal(data, dest)
}

func (s *Store) run(sql string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd := exec.Command(s.sqlite, s.path)
	cmd.Stdin = strings.NewReader(".timeout 5000\n" + sql)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("sqlite3 %s: %w: %s", s.path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func ftsQuery(query string) string {
	raw := strings.Fields(query)
	if len(raw) == 0 {
		return ""
	}
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.Trim(part, "\"'`.,;:!?()[]{}")
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 8 {
		parts = parts[:8]
	}
	for i, part := range parts {
		part = strings.ReplaceAll(part, `"`, `""`)
		parts[i] = `"` + part + `"`
	}
	return strings.Join(parts, " ")
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func sqlNullable(value string) string {
	if strings.TrimSpace(value) == "" {
		return "NULL"
	}
	return sqlString(value)
}

func joinSQL(values ...string) string {
	return strings.Join(values, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
