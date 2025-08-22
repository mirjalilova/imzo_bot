package imzo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"imzoai-telebot/internal/config"
)

type Client struct {
	cfg config.Config
	cli *http.Client
}

func NewClient(cfg config.Config) *Client {
	return &Client{
		cfg: cfg,
		cli: &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSeconds) * time.Second},
	}
}

func (c *Client) DoLogin(login, password string) (string, error) {
	endpoint := strings.TrimRight(c.cfg.ImzoAPIBase, "/") + "/users/login"
	payload := LoginReq{Login: login, Password: password}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}
	var lr LoginResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", err
	}
	if lr.Token == "" {
		return "", errors.New("empty token in login response")
	}
	return lr.Token, nil
}

func (c *Client) Ask(token, chatRoomID, question string) (*AskRespOK, *AskRespErr, error) {
	endpoint := strings.TrimRight(c.cfg.ImzoAPIBase, "/") + "/ask"
	payload := AskReq{ChatRoomID: chatRoomID, Request: question}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)

	resp, err := c.cli.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		var er AskRespErr
		if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
			return nil, nil, err
		}
		return nil, &er, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var ok AskRespOK
	if err := json.NewDecoder(resp.Body).Decode(&ok); err != nil {
		return nil, nil, err
	}
	return &ok, nil, nil
}

func (c *Client) PollFinal(ctx context.Context, userToken, id string) (string, error) {
	endpoint := strings.TrimRight(c.cfg.GatewayBase, "/") + "/get/gpt/responce"
	q := url.Values{"id": {id}}

	authHeader := c.cfg.GatewayAuthBearer
	if strings.TrimSpace(authHeader) == "" {
		authHeader = userToken
	}

	ticker := time.NewTicker(time.Duration(c.cfg.PollIntervalSec))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			req, _ := http.NewRequest(http.MethodGet, endpoint+"?"+q.Encode(), nil)
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", authHeader)

			resp, err := c.cli.Do(req)
			if err != nil {
				continue
			}
			func() {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return
				}
				var fr FinalResp
				if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
					return
				}
				if strings.TrimSpace(fr.Response) != "" {
					ctx.Done()
					return
				}
			}()
		}
	}
}
