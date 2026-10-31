package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The API returns at most this many accounts per request.
const accountsPerRequest = 40

// avatars and display names come from the instance: its API already resolves
// media paths and hides accounts that shouldn't appear.
type person struct {
	ID       string `json:"id"`
	Acct     string `json:"acct"`
	Name     string `json:"display_name"`
	URL      string `json:"url"`
	Avatar   string `json:"avatar_static"`
	Username string `json:"username"`
}

type instanceAPI struct {
	base   string
	domain string
	client *http.Client
}

// people returns the accounts in the order asked for, skipping missing ones.
func (a *instanceAPI) people(ctx context.Context, ids []int64) ([]person, error) {
	found := map[string]person{}
	for start := 0; start < len(ids); start += accountsPerRequest {
		end := min(start+accountsPerRequest, len(ids))
		q := url.Values{}
		for _, id := range ids[start:end] {
			q.Add("id[]", strconv.FormatInt(id, 10))
		}
		batch, err := a.fetch(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, p := range batch {
			if !strings.Contains(p.Acct, "@") {
				p.Acct += "@" + a.domain // local accounts come without it
			}
			found[p.ID] = p
		}
	}
	people := make([]person, 0, len(ids))
	for _, id := range ids {
		if p, ok := found[strconv.FormatInt(id, 10)]; ok {
			people = append(people, p)
		}
	}
	return people, nil
}

func (a *instanceAPI) fetch(ctx context.Context, q url.Values) ([]person, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+"/api/v1/accounts?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instance answered %s", resp.Status)
	}
	var batch []person
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&batch); err != nil {
		return nil, err
	}
	return batch, nil
}

var customEmoji = regexp.MustCompile(`:[a-zA-Z0-9_]+:`)

// DisplayName drops custom emoji shortcodes, which only render on the instance.
func (p person) DisplayName() string {
	if n := strings.TrimSpace(customEmoji.ReplaceAllString(p.Name, "")); n != "" {
		return n
	}
	return p.Username
}
