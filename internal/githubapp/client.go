// Package githubapp implements the bounded GitHub.com API operations used by the
// publisher connection wizard. Credentials remain in memory and errors never
// include provider bodies, request URLs, or underlying transport error text.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type App struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	PEM  string `json:"-"`
}
type Repository struct {
	ID             int64  `json:"id"`
	FullName       string `json:"fullName"`
	DefaultBranch  string `json:"defaultBranch"`
	InstallationID int64  `json:"installationId"`
}
type Inspection struct {
	Supported bool     `json:"supported"`
	Problems  []string `json:"problems"`
}

var ErrConflict = errors.New("GitHub repository configuration conflicts with existing settings")

const (
	maxBody          = 2 << 20
	requestTimeout   = 15 * time.Second
	operationTimeout = 2 * time.Minute
	maxRequests      = 200
	maxPages         = 20
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
var fullName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

type Client struct{ httpClient *http.Client }

// NewClient preserves the injected transport but enforces timeouts and disallows
// all redirects, including same-origin redirects. It does not mutate its input.
func NewClient(httpClient *http.Client) *Client {
	var h http.Client
	if httpClient != nil {
		h = *httpClient
	}
	if h.Timeout <= 0 || h.Timeout > requestTimeout {
		h.Timeout = requestTimeout
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h.Jar = nil
	return &Client{httpClient: &h}
}

type operation struct {
	client   *Client
	ctx      context.Context
	requests int
	next     bool
}

func (c *Client) begin(ctx context.Context) (*operation, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	return &operation{client: c, ctx: ctx}, cancel
}
func (o *operation) request(method, path, token string, input, output any, statuses ...int) (int, error) {
	o.requests++
	if o.requests > maxRequests {
		return 0, errors.New("GitHub request limit exceeded")
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n") {
		return 0, errors.New("invalid GitHub request")
	}
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil || len(body) > maxBody {
			return 0, errors.New("invalid GitHub request body")
		}
	}
	ctx, cancel := context.WithTimeout(o.ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("invalid GitHub request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Services-publisher")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := o.client.httpClient.Do(req)
	if err != nil {
		return 0, errors.New("GitHub request failed")
	}
	defer resp.Body.Close()
	o.next = strings.Contains(resp.Header.Get("Link"), `rel="next"`)
	accepted := false
	for _, s := range statuses {
		if resp.StatusCode == s {
			accepted = true
		}
	}
	if !accepted {
		return resp.StatusCode, fmt.Errorf("GitHub request rejected (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return resp.StatusCode, errors.New("GitHub response unreadable or too large")
	}
	if output != nil && resp.StatusCode != http.StatusNotFound {
		if err = json.Unmarshal(data, output); err != nil {
			return resp.StatusCode, errors.New("GitHub returned an invalid response")
		}
	}
	return resp.StatusCode, nil
}

func privateKey(app App) (*rsa.PrivateKey, error) {
	invalid := errors.New("invalid GitHub App credentials")
	if app.ID <= 0 || !identifier.MatchString(app.Slug) || len(app.PEM) > 32<<10 {
		return nil, invalid
	}
	block, rest := pem.Decode([]byte(app.PEM))
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, invalid
	}
	var key *rsa.PrivateKey
	if block.Type == "RSA PRIVATE KEY" {
		key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	} else if block.Type == "PRIVATE KEY" {
		parsed, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	}
	if key == nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, invalid
	}
	return key, nil
}
func appJWT(app App) (string, error) {
	key, err := privateKey(app)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	payload, _ := json.Marshal(map[string]any{"iss": app.ID, "iat": now - 60, "exp": now + 540})
	text := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(text))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("GitHub App signing failed")
	}
	return text + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
func (c *Client) ExchangeManifest(ctx context.Context, code string) (App, error) {
	if !identifier.MatchString(code) {
		return App{}, errors.New("invalid GitHub manifest code")
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	var v struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
		PEM  string `json:"pem"`
	}
	if _, err := o.request("POST", "/app-manifests/"+code+"/conversions", "", nil, &v, 201); err != nil {
		return App{}, err
	}
	app := App{ID: v.ID, Slug: v.Slug, Name: v.Name, PEM: v.PEM}
	if _, err := privateKey(app); err != nil {
		return App{}, err
	}
	return app, nil
}
func (c *Client) Repositories(ctx context.Context, app App) ([]Repository, error) {
	token, err := appJWT(app)
	if err != nil {
		return nil, err
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	var installations []int64
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, errors.New("GitHub installation page limit exceeded")
		}
		var batch []struct {
			ID int64 `json:"id"`
		}
		if _, err = o.request("GET", "/app/installations?per_page=100&page="+strconv.Itoa(page), token, nil, &batch, 200); err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, errors.New("GitHub returned an invalid installation page")
		}
		for _, v := range batch {
			if v.ID <= 0 {
				return nil, errors.New("invalid GitHub installation")
			}
			installations = append(installations, v.ID)
		}
		if !o.next && len(batch) < 100 {
			break
		}
	}
	repositories := []Repository{}
	seen := map[int64]bool{}
	for _, id := range installations {
		installationToken, err := o.installationToken(token, id, nil, map[string]string{"metadata": "read"})
		if err != nil {
			return nil, err
		}
		for page := 1; ; page++ {
			if page > maxPages {
				return nil, errors.New("GitHub repository page limit exceeded")
			}
			var batch struct {
				Repositories []struct {
					ID            int64  `json:"id"`
					FullName      string `json:"full_name"`
					DefaultBranch string `json:"default_branch"`
				} `json:"repositories"`
			}
			if _, err = o.request("GET", "/installation/repositories?per_page=100&page="+strconv.Itoa(page), installationToken, nil, &batch, 200); err != nil {
				return nil, err
			}
			if batch.Repositories == nil {
				return nil, errors.New("GitHub returned an invalid repository page")
			}
			for _, v := range batch.Repositories {
				repo := Repository{ID: v.ID, FullName: v.FullName, DefaultBranch: v.DefaultBranch, InstallationID: id}
				if !validRepository(repo) {
					return nil, errors.New("invalid GitHub repository")
				}
				if !seen[repo.ID] {
					repositories = append(repositories, repo)
					seen[repo.ID] = true
				}
			}
			if !o.next && len(batch.Repositories) < 100 {
				break
			}
		}
	}
	return repositories, nil
}
func validRepository(r Repository) bool {
	return r.ID > 0 && r.InstallationID > 0 && len(r.FullName) <= 300 && fullName.MatchString(r.FullName) && len(r.DefaultBranch) > 0 && len(r.DefaultBranch) <= 255 && !strings.ContainsAny(r.DefaultBranch, "\x00\r\n")
}
func (o *operation) installationToken(jwt string, id int64, repositoryIDs []int64, permissions map[string]string) (string, error) {
	if len(permissions) == 0 {
		return "", errors.New("explicit GitHub token permissions are required")
	}
	input := map[string]any{"permissions": permissions}
	if repositoryIDs != nil {
		input["repository_ids"] = repositoryIDs
	}
	var v struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if _, err := o.request("POST", "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens", jwt, input, &v, 201); err != nil {
		return "", err
	}
	if len(v.Token) == 0 || len(v.Token) > 4096 || strings.ContainsAny(v.Token, "\r\n\x00 ") || !v.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return "", errors.New("invalid GitHub installation credentials")
	}
	return v.Token, nil
}
