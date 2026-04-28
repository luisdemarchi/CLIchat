package hostclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const DefaultHTTPAddress = "http://127.0.0.1:47657"

type Client struct {
	baseURL string
	http    *http.Client
}

type StartInput struct {
	SessionID string   `json:"sessionId"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	CWD       string   `json:"cwd"`
}

type StartResult struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
}

type SendInput struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
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

func (c *Client) Start(ctx context.Context, input StartInput) (StartResult, error) {
	var output StartResult
	body, err := json.Marshal(input)
	if err != nil {
		return output, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/sessions", bytes.NewReader(body))
	if err != nil {
		return output, err
	}
	req.Header.Set("content-type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return output, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return output, decodeError(res)
	}
	if err := json.NewDecoder(res.Body).Decode(&output); err != nil {
		return output, err
	}
	return output, nil
}

func (c *Client) Send(ctx context.Context, sessionID string, text string) error {
	body, err := json.Marshal(SendInput{Text: text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/sessions/"+sessionID+"/send", bytes.NewReader(body))
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
	return nil
}

func (c *Client) SendData(ctx context.Context, sessionID string, data []byte) error {
	body, err := json.Marshal(SendInput{Data: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/sessions/"+sessionID+"/send", bytes.NewReader(body))
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
	return nil
}

func (c *Client) Subscribe(ctx context.Context, sessionID string, onEvent func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/sessions/"+sessionID+"/events", nil)
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

func decodeError(res *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err == nil && payload.Error != "" {
		return errors.New(payload.Error)
	}
	return fmt.Errorf("host returned %s", res.Status)
}
