package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	maxFetchBytes = 80_000
	defaultSearch = 8
	soMaxSearch   = 3
)

var webClient = &http.Client{Timeout: 15 * time.Second}

var defaultWebHeaders = map[string]string{
	"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Accept-Language": "en-US,en;q=0.9",
}

var (
	hnItemRE      = regexp.MustCompile(`news\.ycombinator\.com/item\?id=(\d+)`)
	githubIssueRE = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	soQuestionRE  = regexp.MustCompile(`stackoverflow\.com/questions/(\d+)`)
	blockTagRE    = regexp.MustCompile(`(?i)</?(?:br|p|div|h[1-6]|li|tr|pre|blockquote|ul|ol)[^>]*>`)
	anyTagRE      = regexp.MustCompile(`<[^>]+>`)
	multiSpaceRE  = regexp.MustCompile(`[^\S\n]+`)
	multiNLRE     = regexp.MustCompile(`\n{3,}`)
	inlineSpaceRE = regexp.MustCompile(`[ \t]+`)
)

var skipHTMLTags = map[string]bool{
	"script": true, "style": true, "nav": true, "header": true,
	"footer": true, "aside": true, "noscript": true, "iframe": true,
	"svg": true, "form": true,
}

var blockHTMLTags = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "li": true, "tr": true,
	"blockquote": true, "pre": true, "article": true, "section": true,
}

type soAnswer struct {
	Body       string
	Score      int
	IsAccepted bool
	QuestionID int
}

// WebSearch searches DuckDuckGo and returns title+url+snippet per result.
func WebSearch(ctx context.Context, query string, maxResults int) ToolResult {
	if maxResults <= 0 {
		maxResults = defaultSearch
	}
	body, err := webGet(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(query), map[string]string{
		"Accept": "text/html,application/xhtml+xml",
	})
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[search error] %v", err), IsError: true}
	}
	results := parseDDGHTML(string(body), maxResults)
	if len(results) == 0 {
		return ToolResult{Output: "[no results]"}
	}
	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "[%d] %s\n    %s\n", i+1, r.title, r.u)
		if r.snippet != "" {
			snip := r.snippet
			if len(snip) > 200 {
				snip = snip[:200]
			}
			fmt.Fprintf(&sb, "    %s\n", snip)
		}
		sb.WriteString("\n")
	}
	return ToolResult{Output: strings.TrimSpace(sb.String())}
}

type ddgResult struct{ title, u, snippet string }

func parseDDGHTML(body string, max int) []ddgResult {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var results []ddgResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= max {
			return
		}
		if n.Type == html.ElementNode && hasHTMLClass(n, "result__body") {
			if r, ok := extractDDGResult(n); ok {
				results = append(results, r)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

func extractDDGResult(n *html.Node) (ddgResult, bool) {
	var r ddgResult
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "a" && hasHTMLClass(node, "result__a") {
				r.title = strings.TrimSpace(nodeText(node))
				r.u = cleanDDGHref(htmlAttr(node, "href"))
			}
			if hasHTMLClass(node, "result__snippet") {
				r.snippet = strings.TrimSpace(nodeText(node))
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return r, r.title != "" && r.u != ""
}

// cleanDDGHref unwraps DuckDuckGo's /l/?uddg= redirect into the real target URL.
// DDG result links look like: //duckduckgo.com/l/?uddg=<urlencoded-target>&rut=...
func cleanDDGHref(href string) string {
	if href == "" {
		return href
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if strings.Contains(u.Host, "duckduckgo.com") && strings.HasPrefix(u.Path, "/l/") {
		if target := u.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return href
}

// WebFetch fetches a URL and returns its main text.
// Handles HN, Reddit, GitHub issues, Stack Overflow, and generic HTML.
func WebFetch(ctx context.Context, rawURL string) ToolResult {
	switch {
	case hnItemRE.MatchString(rawURL):
		return fetchHN(ctx, rawURL)
	case strings.Contains(rawURL, "reddit.com"):
		return fetchReddit(ctx, rawURL)
	case githubIssueRE.MatchString(rawURL):
		return fetchGitHubIssue(ctx, rawURL)
	case soQuestionRE.MatchString(rawURL):
		return fetchSOQuestion(ctx, rawURL)
	default:
		return fetchGeneric(ctx, rawURL)
	}
}

func fetchHN(ctx context.Context, rawURL string) ToolResult {
	m := hnItemRE.FindStringSubmatch(rawURL)
	body, err := webGet(ctx, "https://hn.algolia.com/api/v1/items/"+m[1], nil)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[hn error] %v", err), IsError: true}
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return ToolResult{Output: fmt.Sprintf("[hn parse error] %v", err), IsError: true}
	}
	return ToolResult{Output: truncateWebBytes(formatHN(data))}
}

func formatHN(data map[string]any) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\nby %s | %v points | %s\n\n", jsonStr(data, "title"), jsonStr(data, "author"), data["points"], jsonStr(data, "url"))
	if text := jsonStr(data, "text"); text != "" {
		fmt.Fprintf(&sb, "%s\n\n", stripHTML(text))
	}
	var walkC func(map[string]any, int)
	walkC = func(node map[string]any, depth int) {
		if jsonStr(node, "type") == "comment" {
			fmt.Fprintf(&sb, "%s[%s] %s\n", strings.Repeat("  ", depth), jsonStr(node, "author"), stripHTML(jsonStr(node, "text")))
		}
		for _, child := range jsonSlice(node, "children") {
			if m, ok := child.(map[string]any); ok {
				walkC(m, depth+1)
			}
		}
	}
	for _, child := range jsonSlice(data, "children") {
		if m, ok := child.(map[string]any); ok {
			walkC(m, 0)
		}
	}
	return sb.String()
}

func fetchReddit(ctx context.Context, rawURL string) ToolResult {
	jsonURL := strings.TrimRight(rawURL, "/") + ".json"
	body, err := webGet(ctx, jsonURL, map[string]string{"Accept": "application/json"})
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[reddit error] %v", err), IsError: true}
	}
	var data []any
	if err := json.Unmarshal(body, &data); err != nil || len(data) < 2 {
		return ToolResult{Output: fmt.Sprintf("[reddit parse error] %v", err), IsError: true}
	}
	return ToolResult{Output: truncateWebBytes(formatReddit(data))}
}

func formatReddit(data []any) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = "[reddit parse error]"
		}
	}()
	var sb strings.Builder
	post := nestedMap(data, 0, "data", "children", 0, "data")
	fmt.Fprintf(&sb, "# %s\nby u/%s | score %v\n\n", jsonStr(post, "title"), jsonStr(post, "author"), post["score"])
	if text := jsonStr(post, "selftext"); text != "" {
		fmt.Fprintf(&sb, "%s\n\n", text)
	}
	for i, c := range nestedSlice(data, 1, "data", "children") {
		if i >= 30 {
			break
		}
		d := nestedMap(c, "data")
		if body := jsonStr(d, "body"); body != "" {
			fmt.Fprintf(&sb, "[u/%s | %v] %s\n", jsonStr(d, "author"), d["score"], body)
		}
	}
	return sb.String()
}

func fetchGitHubIssue(ctx context.Context, rawURL string) ToolResult {
	m := githubIssueRE.FindStringSubmatch(rawURL)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%s", m[1], m[2], m[3])
	body, err := webGet(ctx, apiURL, map[string]string{"Accept": "application/vnd.github+json"})
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[github error] %v", err), IsError: true}
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return ToolResult{Output: fmt.Sprintf("[github parse error] %v", err), IsError: true}
	}
	user := jsonMap(data, "user")
	text := fmt.Sprintf("# %s\nby @%s | state: %s\n\n%s",
		jsonStr(data, "title"), jsonStr(user, "login"), jsonStr(data, "state"), stripHTML(jsonStr(data, "body")))
	return ToolResult{Output: truncateWebBytes(text)}
}

func fetchSOQuestion(ctx context.Context, rawURL string) ToolResult {
	m := soQuestionRE.FindStringSubmatch(rawURL)
	qID := m[1]
	body, err := webGet(ctx, soAPIURL("questions/"+qID, "filter=withbody"), nil)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[so error] %v", err), IsError: true}
	}
	var qResp struct {
		Items []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Score int    `json:"score"`
			Link  string `json:"link"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &qResp); err != nil || len(qResp.Items) == 0 {
		return fetchGeneric(ctx, rawURL)
	}
	q := qResp.Items[0]
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\nScore: %d | %s\n\n%s\n\n", q.Title, q.Score, q.Link, stripHTML(q.Body))
	if aBody, err := webGet(ctx, soAPIURL("questions/"+qID+"/answers", "order=desc&sort=votes&filter=withbody&pagesize=3"), nil); err == nil {
		var aResp struct {
			Items []struct {
				Body       string `json:"body"`
				Score      int    `json:"score"`
				IsAccepted bool   `json:"is_accepted"`
			} `json:"items"`
		}
		if json.Unmarshal(aBody, &aResp) == nil {
			for i, a := range aResp.Items {
				accepted := ""
				if a.IsAccepted {
					accepted = " [ACCEPTED]"
				}
				fmt.Fprintf(&sb, "## Answer %d%s (score: %d)\n%s\n\n", i+1, accepted, a.Score, stripHTML(a.Body))
			}
		}
	}
	return ToolResult{Output: truncateWebBytes(sb.String())}
}

// StackOverflowSearch searches SO and returns questions with top answers inline.
func StackOverflowSearch(ctx context.Context, query string, maxResults int) ToolResult {
	if maxResults <= 0 {
		maxResults = soMaxSearch
	}
	params := fmt.Sprintf("q=%s&order=desc&sort=relevance&pagesize=%d&filter=withbody", url.QueryEscape(query), maxResults)
	body, err := webGet(ctx, soAPIURL("search/advanced", params), nil)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[so search error] %v", err), IsError: true}
	}
	var resp struct {
		Items []struct {
			QuestionID  int    `json:"question_id"`
			Title       string `json:"title"`
			Body        string `json:"body"`
			Score       int    `json:"score"`
			AnswerCount int    `json:"answer_count"`
			IsAnswered  bool   `json:"is_answered"`
			Link        string `json:"link"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ToolResult{Output: fmt.Sprintf("[so parse error] %v", err), IsError: true}
	}
	if len(resp.Items) == 0 {
		return ToolResult{Output: "[no SO results]"}
	}

	qIDs := make([]string, len(resp.Items))
	for i, q := range resp.Items {
		qIDs[i] = strconv.Itoa(q.QuestionID)
	}
	answersByQ := map[int][]soAnswer{}
	ansParams := "order=desc&sort=votes&filter=withbody&pagesize=2"
	if aBody, err := webGet(ctx, soAPIURL("questions/"+strings.Join(qIDs, ";")+"/answers", ansParams), nil); err == nil {
		var aResp struct {
			Items []struct {
				Body       string `json:"body"`
				Score      int    `json:"score"`
				IsAccepted bool   `json:"is_accepted"`
				QuestionID int    `json:"question_id"`
			} `json:"items"`
		}
		if json.Unmarshal(aBody, &aResp) == nil {
			for _, a := range aResp.Items {
				answersByQ[a.QuestionID] = append(answersByQ[a.QuestionID], soAnswer{a.Body, a.Score, a.IsAccepted, a.QuestionID})
			}
		}
	}

	var sb strings.Builder
	for i, q := range resp.Items {
		answered := ""
		if q.IsAnswered {
			answered = " [answered]"
		}
		fmt.Fprintf(&sb, "## [%d] %s%s\nScore: %d | Answers: %d | %s\n\n", i+1, q.Title, answered, q.Score, q.AnswerCount, q.Link)
		if q.Body != "" {
			qText := stripHTML(q.Body)
			if len(qText) > 500 {
				qText = qText[:500] + "..."
			}
			fmt.Fprintf(&sb, "**Question:** %s\n\n", qText)
		}
		for j, a := range answersByQ[q.QuestionID] {
			accepted := ""
			if a.IsAccepted {
				accepted = " [ACCEPTED]"
			}
			aText := stripHTML(a.Body)
			if len(aText) > 800 {
				aText = aText[:800] + "..."
			}
			fmt.Fprintf(&sb, "**Answer %d%s** (score: %d):\n%s\n\n", j+1, accepted, a.Score, aText)
		}
		sb.WriteString("---\n\n")
	}
	return ToolResult{Output: truncateWebBytes(strings.TrimSpace(sb.String()))}
}

func soAPIURL(path, params string) string {
	u := fmt.Sprintf("https://api.stackexchange.com/2.3/%s?site=stackoverflow&%s", path, params)
	if key := os.Getenv("STACK_EXCHANGE_KEY"); key != "" {
		u += "&key=" + url.QueryEscape(key)
	}
	return u
}

func fetchGeneric(ctx context.Context, rawURL string) ToolResult {
	body, err := webGet(ctx, rawURL, nil)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("[fetch error] %v", err), IsError: true}
	}
	text := extractMainText(string(body))
	if text == "" {
		text = "[no text extracted]"
	}
	return ToolResult{Output: truncateWebBytes(text)}
}

func webGet(ctx context.Context, rawURL string, extraHeaders map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range defaultWebHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := webClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(resp.Body)
}

func truncateWebBytes(text string) string {
	b := []byte(text)
	if len(b) <= maxFetchBytes {
		return text
	}
	return fmt.Sprintf("[... truncated; showing last %d bytes ...]\n%s", maxFetchBytes, string(b[len(b)-maxFetchBytes:]))
}

func extractMainText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		s := anyTagRE.ReplaceAllString(htmlContent, " ")
		return strings.TrimSpace(inlineSpaceRE.ReplaceAllString(s, " "))
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if skipHTMLTags[tag] {
				return
			}
			if blockHTMLTags[tag] && sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				sb.WriteString(t)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockHTMLTags[strings.ToLower(n.Data)] {
			sb.WriteString("\n")
		}
	}
	walk(doc)
	text := inlineSpaceRE.ReplaceAllString(sb.String(), " ")
	return strings.TrimSpace(multiNLRE.ReplaceAllString(text, "\n\n"))
}

func stripHTML(s string) string {
	s = blockTagRE.ReplaceAllString(s, "\n")
	s = anyTagRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = multiSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(multiNLRE.ReplaceAllString(s, "\n\n"))
}

func hasHTMLClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func jsonStr(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func jsonMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func jsonSlice(m map[string]any, key string) []any {
	v, _ := m[key].([]any)
	return v
}

func nestedMap(v any, keys ...any) map[string]any {
	for _, key := range keys {
		switch k := key.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	m, _ := v.(map[string]any)
	return m
}

func nestedSlice(v any, keys ...any) []any {
	for _, key := range keys {
		switch k := key.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	s, _ := v.([]any)
	return s
}
