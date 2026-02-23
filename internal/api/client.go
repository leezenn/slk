package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://slack.com/api/"

// Client is a raw HTTP Slack API client.
type Client struct {
	token       string
	httpClient  *http.Client
	userCache   map[string]string // user_id -> display_name
	users       []User            // full user list for reverse lookups
	selfID      string            // authenticated user's ID
	warnedUsers map[string]bool   // user IDs already warned about (avoid spam)
}

// NewClient creates a new Slack API client.
func NewClient(token string) *Client {
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userCache:   make(map[string]string),
		warnedUsers: make(map[string]bool),
	}
}

// AuthTestResult holds the response from Slack's auth.test API.
type AuthTestResult struct {
	User   string `json:"user"`
	Team   string `json:"team"`
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
}

// AuthTest validates the token against Slack's auth.test endpoint.
func (c *Client) AuthTest() (*AuthTestResult, error) {
	body, err := c.post("auth.test", url.Values{})
	if err != nil {
		return nil, err
	}
	var result AuthTestResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing auth.test: %w", err)
	}
	return &result, nil
}

// Identify calls auth.test and caches the authenticated user's ID.
func (c *Client) Identify() error {
	result, err := c.AuthTest()
	if err != nil {
		return err
	}
	c.selfID = result.UserID
	return nil
}

// SelfID returns the authenticated user's ID (requires Identify first).
func (c *Client) SelfID() string {
	return c.selfID
}

// GetPresence fetches a single user's presence (active/away).
func (c *Client) GetPresence(userID string) (string, error) {
	body, err := c.post("users.getPresence", url.Values{"user": {userID}})
	if err != nil {
		return "", err
	}
	var resp struct {
		SlackResponse
		Presence string `json:"presence"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing users.getPresence: %w", err)
	}
	return resp.Presence, nil
}

// SlackResponse is the base response from Slack.
type SlackResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	ResponseMetadata ResponseMetadata `json:"response_metadata,omitempty"`
}

// ResponseMetadata holds pagination cursors.
type ResponseMetadata struct {
	NextCursor string `json:"next_cursor"`
}

// Channel represents a Slack conversation.
type Channel struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	IsChannel    bool    `json:"is_channel"`
	IsGroup      bool    `json:"is_group"`
	IsIM         bool    `json:"is_im"`
	IsMpIM       bool    `json:"is_mpim"`
	IsPrivate    bool    `json:"is_private"`
	IsArchived   bool    `json:"is_archived"`
	NumMembers   int     `json:"num_members"`
	User         string  `json:"user,omitempty"` // for DMs, the other user
	Topic        Topic   `json:"topic,omitempty"`
	Purpose      Purpose `json:"purpose,omitempty"`
	NameNorm     string  `json:"name_normalized"`
}

// Topic is a channel topic.
type Topic struct {
	Value string `json:"value"`
}

// Purpose is a channel purpose.
type Purpose struct {
	Value string `json:"value"`
}

// Message represents a Slack message.
type Message struct {
	Type       string     `json:"type"`
	Subtype    string     `json:"subtype,omitempty"`
	User       string     `json:"user,omitempty"`
	BotID      string     `json:"bot_id,omitempty"`
	Text       string     `json:"text"`
	Ts         string     `json:"ts"`
	ThreadTs   string     `json:"thread_ts,omitempty"`
	ReplyCount  int        `json:"reply_count,omitempty"`
	LatestReply string     `json:"latest_reply,omitempty"`
	Reactions   []Reaction `json:"reactions,omitempty"`
	Files      []File     `json:"files,omitempty"`
	Username   string     `json:"username,omitempty"` // bot username
}

// Reaction on a message.
type Reaction struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

// File attached to a message.
type File struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	URLPrivate         string `json:"url_private"`
	URLPrivateDownload string `json:"url_private_download"`
	Mimetype           string `json:"mimetype"`
	Filetype           string `json:"filetype"`
	PrettyType         string `json:"pretty_type"`
}

// User represents a Slack user.
type User struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	RealName string      `json:"real_name"`
	Profile  UserProfile `json:"profile"`
	Deleted  bool        `json:"deleted"`
	IsBot    bool        `json:"is_bot"`
	Presence string      `json:"presence,omitempty"`
}

// UserProfile is the profile section of a user.
type UserProfile struct {
	DisplayName      string `json:"display_name"`
	RealName         string `json:"real_name"`
	Title            string `json:"title"`
	StatusEmoji      string `json:"status_emoji"`
	StatusText       string `json:"status_text"`
	Image48          string `json:"image_48"`
}

// ReactionsListItem is an item from reactions.list.
type ReactionsListItem struct {
	Type    string  `json:"type"`
	Channel string  `json:"channel"`
	Message Message `json:"message"`
}

// SearchResult represents a search result.
type SearchResult struct {
	Messages SearchMessages `json:"messages"`
}

// SearchMessages wraps search message matches.
type SearchMessages struct {
	Total   int              `json:"total"`
	Matches []SearchMatch    `json:"matches"`
	Paging  SearchPaging     `json:"paging"`
}

// SearchMatch is a single search result match.
type SearchMatch struct {
	Type      string        `json:"type"`
	User      string        `json:"user"`
	Username  string        `json:"username"`
	Text      string        `json:"text"`
	Ts        string        `json:"ts"`
	Channel   SearchChannel `json:"channel"`
	Permalink string        `json:"permalink"`
	Files     []File        `json:"files,omitempty"`
}

// SearchChannel is the channel info in a search result.
type SearchChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SearchPaging holds search pagination info.
type SearchPaging struct {
	Count int `json:"count"`
	Total int `json:"total"`
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

// post makes a POST request to a Slack API method.
func (c *Client) post(method string, params url.Values) ([]byte, error) {
	reqURL := baseURL + method

	req, err := http.NewRequest("POST", reqURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var base SlackResponse
	if err := json.Unmarshal(body, &base); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if !base.OK {
		return nil, fmt.Errorf("slack API error: %s", base.Error)
	}

	return body, nil
}

// doWithRetry executes a request with rate-limit retry.
func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		// Clone request body for retries
		var bodyBytes []byte
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(req.Body)
			req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP request failed: %w", err)
		}

		if resp.StatusCode == 429 {
			retryAfter := resp.Header.Get("Retry-After")
			seconds, _ := strconv.Atoi(retryAfter)
			if seconds == 0 {
				seconds = 1 << i // exponential backoff
			}
			resp.Body.Close()
			time.Sleep(time.Duration(seconds) * time.Second)
			// Restore body for retry
			if bodyBytes != nil {
				req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
			}
			continue
		}

		return resp, nil
	}
	return nil, fmt.Errorf("rate limited: max retries exceeded")
}

// ListChannels returns conversations matching the given types.
func (c *Client) ListChannels(types string, limit int) ([]Channel, error) {
	var all []Channel
	cursor := ""
	pageLimit := 200
	if limit > 0 && limit < pageLimit {
		pageLimit = limit
	}

	for {
		params := url.Values{
			"types":            {types},
			"limit":            {strconv.Itoa(pageLimit)},
			"exclude_archived": {"true"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.post("conversations.list", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Channels []Channel `json:"channels"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Channels...)

		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// GetMembers returns user IDs of members in a channel.
func (c *Client) GetMembers(channelID string) ([]string, error) {
	var all []string
	cursor := ""

	for {
		params := url.Values{
			"channel": {channelID},
			"limit":   {"1000"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.post("conversations.members", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Members []string `json:"members"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Members...)

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// GetHistory retrieves messages from a channel.
func (c *Client) GetHistory(channelID string, limit int, oldest, latest string) ([]Message, error) {
	var all []Message
	cursor := ""
	pageLimit := 100
	if limit > 0 && limit < pageLimit {
		pageLimit = limit
	}

	for {
		params := url.Values{
			"channel": {channelID},
			"limit":   {strconv.Itoa(pageLimit)},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		if oldest != "" {
			params.Set("oldest", oldest)
		}
		if latest != "" {
			params.Set("latest", latest)
		}

		body, err := c.post("conversations.history", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Messages...)

		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// GetHistoryAfter retrieves messages after (and optionally including) a timestamp.
// Uses oldest param with inclusive=true, so the message at `oldest` is included.
// Returns messages newest-first (same as GetHistory).
func (c *Client) GetHistoryAfter(channelID string, limit int, oldest string) ([]Message, error) {
	var all []Message
	cursor := ""
	pageLimit := 100
	if limit > 0 && limit < pageLimit {
		pageLimit = limit
	}

	for {
		params := url.Values{
			"channel":   {channelID},
			"limit":     {strconv.Itoa(pageLimit)},
			"oldest":    {oldest},
			"inclusive":  {"true"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.post("conversations.history", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Messages...)

		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// GetMessage fetches a single message by its exact timestamp.
func (c *Client) GetMessage(channelID, ts string) (*Message, error) {
	params := url.Values{
		"channel":   {channelID},
		"oldest":    {ts},
		"latest":    {ts},
		"limit":     {"1"},
		"inclusive":  {"true"},
	}

	body, err := c.post("conversations.history", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SlackResponse
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	if len(resp.Messages) == 0 {
		return nil, fmt.Errorf("message not found: ts=%s in channel %s", ts, channelID)
	}

	return &resp.Messages[0], nil
}

// GetContext fetches `before` messages older than the given timestamp.
// Returns messages newest-first (same as conversations.history default).
func (c *Client) GetContext(channelID, ts string, before int) ([]Message, error) {
	params := url.Values{
		"channel": {channelID},
		"latest":  {ts},
		"limit":   {strconv.Itoa(before)},
	}

	body, err := c.post("conversations.history", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SlackResponse
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	return resp.Messages, nil
}

// GetReplies retrieves thread replies.
func (c *Client) GetReplies(channelID, threadTs string, limit int) ([]Message, error) {
	var all []Message
	cursor := ""
	pageLimit := 100
	if limit > 0 && limit < pageLimit {
		pageLimit = limit
	}

	for {
		params := url.Values{
			"channel": {channelID},
			"ts":      {threadTs},
			"limit":   {strconv.Itoa(pageLimit)},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.post("conversations.replies", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Messages...)

		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// SearchMessages searches for messages.
func (c *Client) SearchMessages(query string, limit int) (*SearchResult, error) {
	params := url.Values{
		"query":    {query},
		"count":    {strconv.Itoa(limit)},
		"sort":     {"timestamp"},
		"sort_dir": {"desc"},
	}

	body, err := c.post("search.messages", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SlackResponse
		Messages SearchMessages `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	return &SearchResult{Messages: resp.Messages}, nil
}

// ReactionsList returns items the authenticated user has reacted to.
// limit caps total items returned (0 = all).
func (c *Client) ReactionsList(limit int) ([]ReactionsListItem, error) {
	var all []ReactionsListItem
	cursor := ""

	for {
		params := url.Values{
			"full":  {"true"},
			"limit": {"100"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.post("reactions.list", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Items []ReactionsListItem `json:"items"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Items...)

		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// ListUsers returns all workspace users.
func (c *Client) ListUsers() ([]User, error) {
	var all []User
	cursor := ""

	for {
		params := url.Values{
			"limit": {"200"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		body, err := c.post("users.list", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			SlackResponse
			Members []User `json:"members"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		all = append(all, resp.Members...)

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return all, nil
}

// GetUserInfo retrieves info for a single user.
func (c *Client) GetUserInfo(userID string) (*User, error) {
	params := url.Values{
		"user": {userID},
	}

	body, err := c.post("users.info", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SlackResponse
		User User `json:"user"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	return &resp.User, nil
}

// BuildUserCache populates the internal user cache.
func (c *Client) BuildUserCache() error {
	users, err := c.ListUsers()
	if err != nil {
		return err
	}
	c.users = users
	for _, u := range users {
		name := u.Profile.DisplayName
		if name == "" {
			name = u.Name
		}
		c.userCache[u.ID] = name
	}
	return nil
}

// ResolveUser returns a display name for a user ID from cache.
// If the cache is built but the ID is not found, warns once to stderr.
func (c *Client) ResolveUser(userID string) string {
	if name, ok := c.userCache[userID]; ok {
		return name
	}
	if len(c.userCache) > 0 && !c.warnedUsers[userID] {
		fmt.Fprintf(os.Stderr, "Warning: unknown user ID %q not in cache\n", userID)
		c.warnedUsers[userID] = true
	}
	return userID
}

// UserCacheBuilt returns true if the user cache has been populated.
func (c *Client) UserCacheBuilt() bool {
	return len(c.userCache) > 0
}

// GetUserCache returns the internal user cache map.
func (c *Client) GetUserCache() map[string]string {
	return c.userCache
}

// FindChannelByName looks up a channel by name (case-insensitive).
func (c *Client) FindChannelByName(name string) (*Channel, error) {
	types := "public_channel,private_channel,mpim,im"
	channels, err := c.ListChannels(types, 0)
	if err != nil {
		return nil, err
	}

	nameLower := strings.ToLower(name)
	for _, ch := range channels {
		if strings.ToLower(ch.Name) == nameLower {
			return &ch, nil
		}
	}
	return nil, fmt.Errorf("channel not found: %s", name)
}

// FindUserByName finds a user by display_name, username (name), or real_name (case-insensitive).
func (c *Client) FindUserByName(query string) (*User, error) {
	if !c.UserCacheBuilt() {
		if err := c.BuildUserCache(); err != nil {
			return nil, fmt.Errorf("building user cache: %w", err)
		}
	}

	queryLower := strings.ToLower(query)
	for i, u := range c.users {
		if strings.ToLower(u.Profile.DisplayName) == queryLower ||
			strings.ToLower(u.Name) == queryLower ||
			strings.ToLower(u.RealName) == queryLower {
			return &c.users[i], nil
		}
	}
	return nil, fmt.Errorf("user not found: %s", query)
}

// FindDMByUserID finds a DM channel with the given user ID.
func (c *Client) FindDMByUserID(userID string) (*Channel, error) {
	channels, err := c.ListChannels("im", 0)
	if err != nil {
		return nil, err
	}

	for _, ch := range channels {
		if ch.User == userID {
			return &ch, nil
		}
	}
	return nil, fmt.Errorf("no DM channel found with user ID: %s", userID)
}

// FindDMByUser finds a DM channel with the given username or display name.
func (c *Client) FindDMByUser(username string) (*Channel, error) {
	user, err := c.FindUserByName(username)
	if err != nil {
		return nil, err
	}

	return c.FindDMByUserID(user.ID)
}

// ResolveDisplayNameToUsername resolves a display name to the Slack username (name field).
// Returns the original input unchanged if no match is found, but warns to stderr
// when the cache is built and the lookup fails.
func (c *Client) ResolveDisplayNameToUsername(displayName string) string {
	if !c.UserCacheBuilt() {
		return displayName
	}

	user, err := c.FindUserByName(displayName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not resolve display name %q to username\n", displayName)
		return displayName
	}
	return user.Name
}

// DownloadFile downloads a file from a Slack URL.
func (c *Client) DownloadFile(fileURL string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	return resp.Body, resp.ContentLength, nil
}
