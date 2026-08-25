package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://slack.com/api/"

// Client is a raw HTTP Slack API client.
type Client struct {
	token       string
	httpClient  *http.Client
	ctx         context.Context
	errOut      io.Writer
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
		ctx:         context.Background(),
		errOut:      io.Discard,
		userCache:   make(map[string]string),
		warnedUsers: make(map[string]bool),
	}
}

// SetContext binds subsequent requests to the command invocation context.
func (c *Client) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.ctx = ctx
}

// SetErrorWriter routes non-fatal diagnostics through the command stream.
func (c *Client) SetErrorWriter(errOut io.Writer) {
	if errOut == nil {
		errOut = io.Discard
	}
	c.errOut = errOut
}

// AuthTestResult holds the response from Slack's auth.test API.
type AuthTestResult struct {
	User   string   `json:"user"`
	Team   string   `json:"team"`
	TeamID string   `json:"team_id"`
	UserID string   `json:"user_id"`
	Scopes []string `json:"-"`
}

// AuthTest validates the token and reports the scopes Slack granted to it.
func (c *Client) AuthTest() (*AuthTestResult, error) {
	body, headers, err := c.postWithHeaders("auth.test", url.Values{})
	if err != nil {
		return nil, err
	}
	var result AuthTestResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing auth.test: %w", err)
	}
	result.Scopes = parseScopes(headers.Get("X-OAuth-Scopes"))
	return &result, nil
}

func parseScopes(header string) []string {
	seen := make(map[string]struct{})
	for _, scope := range strings.Split(header, ",") {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			seen[scope] = struct{}{}
		}
	}

	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes
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

// MethodError is a definite Slack Web API rejection.
type MethodError struct {
	Code string
}

func (e *MethodError) Error() string {
	if isAuthError(e.Code) {
		return fmt.Sprintf("slack API error: %s\n\nYour token needs refreshing. Get a new one from https://api.slack.com/apps\n(OAuth & Permissions → User OAuth Token) then run: slk auth xoxp-...", e.Code)
	}
	return "slack API error: " + e.Code
}

// ResponseMetadata holds pagination cursors.
type ResponseMetadata struct {
	NextCursor string `json:"next_cursor"`
}

// Channel represents a Slack conversation.
type Channel struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	IsChannel  bool    `json:"is_channel"`
	IsGroup    bool    `json:"is_group"`
	IsIM       bool    `json:"is_im"`
	IsMpIM     bool    `json:"is_mpim"`
	IsPrivate  bool    `json:"is_private"`
	IsArchived bool    `json:"is_archived"`
	NumMembers int     `json:"num_members"`
	User       string  `json:"user,omitempty"` // for DMs, the other user
	Topic      Topic   `json:"topic,omitempty"`
	Purpose    Purpose `json:"purpose,omitempty"`
	NameNorm   string  `json:"name_normalized"`
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
	Type        string     `json:"type"`
	Subtype     string     `json:"subtype,omitempty"`
	User        string     `json:"user,omitempty"`
	BotID       string     `json:"bot_id,omitempty"`
	Text        string     `json:"text"`
	Ts          string     `json:"ts"`
	ThreadTs    string     `json:"thread_ts,omitempty"`
	ReplyCount  int        `json:"reply_count,omitempty"`
	LatestReply string     `json:"latest_reply,omitempty"`
	Reactions   []Reaction `json:"reactions,omitempty"`
	Files       []File     `json:"files,omitempty"`
	Username    string     `json:"username,omitempty"` // bot username
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
	DisplayName string `json:"display_name"`
	RealName    string `json:"real_name"`
	Title       string `json:"title"`
	StatusEmoji string `json:"status_emoji"`
	StatusText  string `json:"status_text"`
	Image48     string `json:"image_48"`
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
	Total   int           `json:"total"`
	Matches []SearchMatch `json:"matches"`
	Paging  SearchPaging  `json:"paging"`
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
	body, _, err := c.postWithHeaders(method, params)
	return body, err
}

func (c *Client) postWithHeaders(method string, params url.Values) ([]byte, http.Header, error) {
	reqURL := baseURL + method

	req, err := http.NewRequestWithContext(c.ctx, "POST", reqURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("reading response: %w", err)
	}

	var base SlackResponse
	if err := json.Unmarshal(body, &base); err != nil {
		return nil, nil, fmt.Errorf("parsing response: %w", err)
	}
	if !base.OK {
		return nil, nil, &MethodError{Code: base.Error}
	}

	return body, resp.Header.Clone(), nil
}

// isAuthError returns true for Slack API errors indicating an invalid or expired token.
func isAuthError(code string) bool {
	switch code {
	case "token_revoked", "token_expired", "invalid_auth", "account_inactive", "not_authed":
		return true
	}
	return false
}

// doWithRetry executes a request with rate-limit retry.
func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
	return c.doWithRetryClient(c.httpClient, req)
}

func (c *Client) doWithRetryClient(httpClient *http.Client, req *http.Request) (*http.Response, error) {
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		// Clone request body for retries
		var bodyBytes []byte
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(req.Body)
			req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
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
			"inclusive": {"true"},
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
		"inclusive": {"true"},
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

// PostMessageResult identifies a message Slack accepted.
type PostMessageResult struct {
	Channel string `json:"channel"`
	Ts      string `json:"ts"`
}

// PostReply posts one message into an existing thread.
func (c *Client) PostReply(channelID, threadTs, text string) (*PostMessageResult, error) {
	body, err := c.post("chat.postMessage", url.Values{
		"channel":   {channelID},
		"thread_ts": {threadTs},
		"text":      {text},
	})
	if err != nil {
		return nil, err
	}
	var result PostMessageResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing chat.postMessage: %w", err)
	}
	if result.Channel == "" || result.Ts == "" {
		return nil, fmt.Errorf("parsing chat.postMessage: successful response omitted channel or timestamp")
	}
	return &result, nil
}

// GetPermalink returns Slack's canonical permalink for a message.
func (c *Client) GetPermalink(channelID, messageTs string) (string, error) {
	body, err := c.post("chat.getPermalink", url.Values{
		"channel":    {channelID},
		"message_ts": {messageTs},
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Permalink string `json:"permalink"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing chat.getPermalink: %w", err)
	}
	if result.Permalink == "" {
		return "", fmt.Errorf("parsing chat.getPermalink: successful response omitted permalink")
	}
	return result.Permalink, nil
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
		fmt.Fprintf(c.errOut, "Warning: unknown user ID %q not in cache\n", userID)
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
		fmt.Fprintf(c.errOut, "Warning: could not resolve display name %q to username\n", displayName)
		return displayName
	}
	return user.Name
}

// GetFileInfo retrieves metadata for a Slack file ID.
func (c *Client) GetFileInfo(fileID string) (*File, error) {
	body, err := c.post("files.info", url.Values{"file": {fileID}})
	if err != nil {
		return nil, err
	}

	var resp struct {
		SlackResponse
		File File `json:"file"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing files.info: %w", err)
	}
	return &resp.File, nil
}

func validateSlackFileURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("invalid Slack file URL")
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("refusing Slack file URL with non-HTTPS scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return fmt.Errorf("refusing Slack file URL containing user information")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return fmt.Errorf("refusing Slack file URL using port %s", port)
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "slack.com" && !strings.HasSuffix(host, ".slack.com") {
		return fmt.Errorf("refusing non-Slack file host %q", host)
	}
	return nil
}

func sanitizeDownloadRequestError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Err == nil {
			return errors.New("request failed")
		}
		return fmt.Errorf("request failed: %w", urlErr.Err)
	}
	return fmt.Errorf("request failed: %w", err)
}

// DownloadFile downloads a file from a trusted Slack URL.
func (c *Client) DownloadFile(fileURL string) (io.ReadCloser, int64, error) {
	if err := validateSlackFileURL(fileURL); err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(c.ctx, "GET", fileURL, nil)
	if err != nil {
		return nil, 0, errors.New("creating Slack file request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	downloadClient := &http.Client{
		Transport: c.httpClient.Transport,
		Timeout:   c.httpClient.Timeout,
		Jar:       c.httpClient.Jar,
	}
	downloadClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 Slack file redirects")
		}
		if err := validateSlackFileURL(req.URL.String()); err != nil {
			return fmt.Errorf("refusing Slack file redirect: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		return nil
	}

	resp, err := c.doWithRetryClient(downloadClient, req)
	if err != nil {
		return nil, 0, fmt.Errorf("downloading Slack file: %w", sanitizeDownloadRequestError(err))
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("downloading Slack file: HTTP %s", resp.Status)
	}

	return resp.Body, resp.ContentLength, nil
}
