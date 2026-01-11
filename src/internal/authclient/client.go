package authclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL     string
	InternalKey string
	HTTPClient  *http.Client
}

func New(baseURL, internalKey string) *Client {
	return &Client{
		BaseURL:     baseURL,
		InternalKey: internalKey,
		HTTPClient:  &http.Client{Timeout: 6 * time.Second},
	}
}

func (c *Client) Login(req LoginRequest) (User, error) {
	var out LoginResponse
	if err := c.doJSON("POST", "/api/login", false, req, &out); err != nil {
		return User{}, err
	}
	return out.User, nil
}

func (c *Client) CreateUser(req CreateUserRequest) (User, error) {
	var out CreateUserResponse
	if err := c.doJSON("POST", "/api/users", true, req, &out); err != nil {
		return User{}, err
	}
	return out.User, nil
}

func (c *Client) ListUsersByRole(role string) ([]User, error) {
	u, _ := url.Parse(c.BaseURL)
	u.Path = "/api/users"
	q := u.Query()
	q.Set("role", role)
	u.RawQuery = q.Encode()

	httpReq, _ := http.NewRequest("GET", u.String(), nil)
	httpReq.Header.Set("X-Internal-Key", c.InternalKey)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("auth list users status=%d", resp.StatusCode)
	}

	var out ListUsersResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

func (c *Client) doJSON(method, path string, internal bool, in any, out any) error {
	b, _ := json.Marshal(in)
	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if internal {
		req.Header.Set("X-Internal-Key", c.InternalKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// try read {error:"..."} but keep simple
		return fmt.Errorf("auth request failed status=%d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
