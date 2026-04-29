package hostclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clichat/internal/agent"
)

const DefaultHTTPAddress = "http://127.0.0.1:47657"

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultHTTPAddress
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("host health failed: %s", res.Status)
	}
	return nil
}

type StateResponse struct {
	Instances []agent.Instance `json:"instances"`
}

func (c *Client) State(ctx context.Context) (StateResponse, error) {
	var out StateResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/state", nil)
	if err != nil {
		return out, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return out, decodeError(res)
	}
	return out, json.NewDecoder(res.Body).Decode(&out)
}

type CreateInstanceInput struct {
	Origin     agent.Origin `json:"origin"`
	ProviderID string       `json:"providerId"`
	Title      string       `json:"title"`
	CWD        string       `json:"cwd"`
}

func (c *Client) CreateInstance(ctx context.Context, input CreateInstanceInput) (agent.Instance, error) {
	if input.Origin == "" {
		input.Origin = agent.OriginInternal
	}
	return c.postInstance(ctx, c.baseURL+"/v1/instances", input)
}

type StartTerminalInput struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
	Env     []string `json:"env,omitempty"`
}

func (c *Client) StartTerminal(ctx context.Context, instanceID string, input StartTerminalInput) (agent.Instance, error) {
	return c.postInstance(ctx, c.baseURL+"/v1/instances/"+instanceID+"/start-terminal", input)
}

func (c *Client) StopTerminal(ctx context.Context, instanceID string) (agent.Instance, error) {
	return c.postInstance(ctx, c.baseURL+"/v1/instances/"+instanceID+"/stop-terminal", map[string]any{})
}

type messagePayload struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func (c *Client) AppendMessage(ctx context.Context, instanceID string, role agent.Role, text string) (agent.Instance, error) {
	return c.postInstance(ctx, c.baseURL+"/v1/instances/"+instanceID+"/message", messagePayload{Role: string(role), Text: text})
}

type statusPayload struct {
	Status string `json:"status"`
	Tool   string `json:"tool"`
}

func (c *Client) SetStatus(ctx context.Context, instanceID string, status agent.Status, tool string) (agent.Instance, error) {
	return c.postInstance(ctx, c.baseURL+"/v1/instances/"+instanceID+"/status", statusPayload{Status: string(status), Tool: tool})
}

type pendingPayload struct {
	Question string                `json:"question"`
	Actions  []agent.PendingAction `json:"actions"`
}

func (c *Client) SetPending(ctx context.Context, instanceID string, question string, actions []agent.PendingAction) (agent.Instance, error) {
	return c.postInstance(ctx, c.baseURL+"/v1/instances/"+instanceID+"/pending", pendingPayload{Question: question, Actions: actions})
}

func (c *Client) ClearPending(ctx context.Context, instanceID string) (agent.Instance, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/v1/instances/"+instanceID+"/pending", nil)
	if err != nil {
		return agent.Instance{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return agent.Instance{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return agent.Instance{}, decodeError(res)
	}
	var out agent.Instance
	return out, json.NewDecoder(res.Body).Decode(&out)
}

type SendInput struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
}

func (c *Client) SendText(ctx context.Context, instanceID string, text string) error {
	return c.postNoResult(ctx, c.baseURL+"/v1/instances/"+instanceID+"/send", SendInput{Text: text})
}

func (c *Client) SendData(ctx context.Context, instanceID string, data []byte) error {
	return c.postNoResult(ctx, c.baseURL+"/v1/instances/"+instanceID+"/send", SendInput{Data: base64.StdEncoding.EncodeToString(data)})
}

type Event struct {
	Type  string `json:"type"`
	Data  []byte `json:"data"`
	Error string `json:"error,omitempty"`
}

type wireEvent struct {
	Type  string `json:"type"`
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func (c *Client) SubscribeOutput(ctx context.Context, instanceID string, onEvent func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/instances/"+instanceID+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "text/event-stream")
	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return decodeError(res)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var wire wireEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &wire); err != nil {
			continue
		}
		event := Event{Type: wire.Type, Error: wire.Error}
		if wire.Data != "" {
			if decoded, err := base64.StdEncoding.DecodeString(wire.Data); err == nil {
				event.Data = decoded
			}
		}
		onEvent(event)
	}
	return scanner.Err()
}

type StateEvent struct {
	Kind    string `json:"kind"`
	Payload json.RawMessage
}

type wireState struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func (c *Client) SubscribeState(ctx context.Context, onEvent func(StateEvent)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/state/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "text/event-stream")
	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return decodeError(res)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var wire wireState
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &wire); err != nil {
			continue
		}
		onEvent(StateEvent{Kind: wire.Kind, Payload: wire.Payload})
	}
	return scanner.Err()
}

func (c *Client) postInstance(ctx context.Context, url string, payload any) (agent.Instance, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return agent.Instance{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return agent.Instance{}, err
	}
	req.Header.Set("content-type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return agent.Instance{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return agent.Instance{}, decodeError(res)
	}
	var out agent.Instance
	return out, json.NewDecoder(res.Body).Decode(&out)
}

func (c *Client) postNoResult(ctx context.Context, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return decodeError(res)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

func decodeError(res *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err == nil && payload.Error != "" {
		return errors.New(payload.Error)
	}
	return fmt.Errorf("host returned %s", res.Status)
}
