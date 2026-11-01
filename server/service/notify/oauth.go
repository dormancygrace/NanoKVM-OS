package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// OAuth 2.0 sign-in of the e-mail channel (SMTP XOAUTH2). The OAuth client is
// the user's own registration. Google allows no device flow for mail, so its
// sign-in is an authorization code with PKCE and a loopback address: nothing
// listens there, and the user pastes the address the browser ends on.
// Microsoft takes the device code flow of a public client. The refresh token
// stays on the device; access tokens live in memory only.

// The services' addresses; tests point them at fakes.
var (
	googleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL  = "https://oauth2.googleapis.com/token"
	googleRevokeURL = "https://oauth2.googleapis.com/revoke"
	microsoftURL    = "https://login.microsoftonline.com"
)

const (
	googleScope    = "https://mail.google.com/ openid email"
	microsoftScope = "https://outlook.office.com/SMTP.Send offline_access openid email profile"
	// An access token is renewed this long before it expires.
	tokenSlack = time.Minute
	// A sign-in on the page and its result wait this long; at most
	// maxFlows sign-ins run, and a result is kept for each channel.
	flowTTL    = 15 * time.Minute
	pendingTTL = time.Hour
	maxFlows   = 4
	// pendingPrefix marks a sign-in not saved yet in the oauthToken field.
	pendingPrefix = "pending:"
)

// authError is a failed sign-in: the channel shows it until the user signs
// in again or a message goes through.
type authError struct{ err error }

func (e authError) Error() string { return e.err.Error() }
func (e authError) Unwrap() error { return e.err }

type oauthClient struct {
	kind, id, secret, tenant string
}

func clientOf(ch Channel) (oauthClient, error) {
	p := providerByID(ch.get("provider"))
	c := oauthClient{kind: p.OAuth, id: ch.get("oauthClientId"), secret: ch.get("oauthClientSecret"), tenant: ch.get("oauthTenant")}
	if c.tenant == "" {
		c.tenant = p.Tenant
	}
	switch {
	case c.kind == "":
		return c, permanent(errors.New("OAuth sign-in is for Gmail and Microsoft accounts"))
	case c.id == "":
		return c, permanent(errors.New("the OAuth client ID is missing"))
	case c.kind == oauthGoogle && c.secret == "":
		return c, permanent(errors.New("the OAuth client secret is missing"))
	case c.kind == oauthMicrosoft && !tenantName.MatchString(c.tenant):
		return c, permanent(errors.New("the tenant is not a tenant ID or domain"))
	}
	return c, nil
}

func (c oauthClient) tokenURL() string {
	if c.kind == oauthGoogle {
		return googleTokenURL
	}
	return microsoftURL + "/" + c.tenant + "/oauth2/v2.0/token"
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	// The device code flow.
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	// An error answer (RFC 6749 5.2).
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// oauthErr is an error answer of a sign-in service.
type oauthErr struct{ code, description string }

func (e oauthErr) Error() string {
	text := e.description
	// Microsoft adds trace and correlation ids on further lines.
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	switch {
	case strings.Contains(text, "AADSTS7000218"):
		return "the app registration must allow public client flows (Authentication > Advanced settings)"
	case strings.Contains(text, "AADSTS50194"):
		return "the app is for one organisation only: enter its tenant ID"
	case e.code == "invalid_grant":
		return "the sign-in has expired or was revoked; sign in again"
	case text == "":
		return e.code
	}
	return e.code + ": " + text
}

var oauthHTTP = &http.Client{Timeout: sendTimeout}

func postForm(ctx context.Context, target string, form url.Values) (tokenResponse, error) {
	var answer tokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return answer, permanent(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "NanoKVM")
	resp, err := oauthHTTP.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return answer, urlErr.Err
		}
		return answer, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err := json.Unmarshal(body, &answer); err != nil {
		return answer, fmt.Errorf("HTTP %d from the sign-in service", resp.StatusCode)
	}
	if answer.Error != "" {
		err := oauthErr{answer.Error, answer.Description}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return answer, permanent(err)
		}
		return answer, err
	}
	if resp.StatusCode != http.StatusOK {
		return answer, fmt.Errorf("HTTP %d from the sign-in service", resp.StatusCode)
	}
	return answer, nil
}

func refreshToken(ctx context.Context, c oauthClient, refresh string) (tokenResponse, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {c.id}, "refresh_token": {refresh}}
	if c.kind == oauthGoogle {
		form.Set("client_secret", c.secret)
	} else {
		form.Set("scope", microsoftScope)
	}
	answer, err := postForm(ctx, c.tokenURL(), form)
	if err == nil && answer.AccessToken == "" {
		err = errors.New("the sign-in service sent no access token")
	}
	return answer, err
}

// revoke ends a Google sign-in. Microsoft has no revocation for a public
// client: its users remove the app from their account.
func revoke(c oauthClient, refresh string) {
	if c.kind != oauthGoogle || refresh == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleRevokeURL, strings.NewReader(url.Values{"token": {refresh}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := oauthHTTP.Do(req)
	if err != nil {
		log.Debugf("notifications: revoking a Google sign-in failed: %v", err)
		return
	}
	resp.Body.Close()
}

// accountOf reads the mailbox address from the ID token; it came straight
// from the token endpoint over TLS, so its signature is not checked.
func accountOf(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	// Microsoft often leaves email out; preferred_username is the address
	// the account signs in with.
	if claims.Email != "" {
		return claims.Email
	}
	return claims.PreferredUsername
}

// tokenEntry is the access token of one channel. base is the stored refresh
// token it came from, refresh the newest one (Microsoft sends a new one with
// every access token).
type tokenEntry struct {
	mu                    sync.Mutex
	base, refresh, access string
	expiry                time.Time
}

// flow is a sign-in started on the page.
type flow struct {
	client                    oauthClient
	verifier, redirect, state string
	deviceCode                string
	interval                  int
	expires                   time.Time
}

// pendingSignIn is a finished sign-in the page has not saved yet. kept: it
// was saved; the handle still resolves for a page that sends it again.
type pendingSignIn struct {
	client  oauthClient
	token   string
	expires time.Time
	kept    bool
}

type oauthState struct {
	mu      sync.Mutex
	entries map[string]*tokenEntry
	flows   map[string]*flow
	pending map[string]*pendingSignIn
	// persist stores a rotated refresh token in the channel of id if it
	// still holds old; it reports whether it did.
	persist func(id, old, new string) bool
	now     func() time.Time
}

var oauth = &oauthState{
	entries: map[string]*tokenEntry{},
	flows:   map[string]*flow{},
	pending: map[string]*pendingSignIn{},
	persist: func(string, string, string) bool { return false },
	now:     time.Now,
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// entryKey: a saved channel by its id, one on the page by its token.
func entryKey(ch Channel) string {
	if ch.ID != "" {
		return ch.ID
	}
	return "t" + hashOf(ch.get("oauthToken"))
}

// accessToken gives a valid access token for the channel, renewing it when
// it is about to expire. Deliveries of one channel renew it once.
func (o *oauthState) accessToken(ctx context.Context, ch Channel) (string, error) {
	stored := ch.get("oauthToken")
	if stored == "" || strings.HasPrefix(stored, pendingPrefix) {
		return "", permanent(authError{errors.New("not signed in")})
	}
	client, err := clientOf(ch)
	if err != nil {
		return "", err
	}
	key := entryKey(ch)
	o.mu.Lock()
	e := o.entries[key]
	if e == nil || e.base != stored {
		e = &tokenEntry{base: stored, refresh: stored}
		o.entries[key] = e
	}
	o.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.access != "" && o.now().Add(tokenSlack).Before(e.expiry) {
		return e.access, nil
	}
	answer, err := refreshToken(ctx, client, e.refresh)
	if err != nil {
		var oe oauthErr
		if errors.As(err, &oe) && oe.code == "invalid_grant" {
			return "", permanent(authError{err})
		}
		return "", err
	}
	e.access = answer.AccessToken
	e.expiry = o.now().Add(time.Duration(answer.ExpiresIn) * time.Second)
	if answer.RefreshToken != "" && answer.RefreshToken != e.refresh {
		e.refresh = answer.RefreshToken
		if ch.ID != "" && o.persist(ch.ID, e.base, answer.RefreshToken) {
			// base is read under o.mu too.
			o.mu.Lock()
			e.base = answer.RefreshToken
			o.mu.Unlock()
		}
	}
	return e.access, nil
}

// forget drops the access token of a channel, after the mail server refused
// it: the next attempt renews it.
func (o *oauthState) forget(ch Channel) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if e := o.entries[entryKey(ch)]; e != nil && e.base == ch.get("oauthToken") {
		delete(o.entries, entryKey(ch))
	}
}

// expire drops old flows and pending sign-ins; it holds o.mu. A sign-in
// never saved is revoked.
func (o *oauthState) expire() {
	now := o.now()
	for id, f := range o.flows {
		if now.After(f.expires) {
			delete(o.flows, id)
		}
	}
	for handle, p := range o.pending {
		if now.After(p.expires) {
			delete(o.pending, handle)
			if !p.kept {
				go revoke(p.client, p.token)
			}
		}
	}
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// FlowStart is a sign-in for the page to show: a code to enter at an
// address (device), or an address to open whose final address the user
// pastes back (code).
type FlowStart struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	UserCode string `json:"userCode,omitempty"`
	Interval int    `json:"interval,omitempty"`
	Expires  int    `json:"expiresIn"`
}

// FlowResult: Pending while the device code waits for the user; else Token
// is the handle the page puts in the oauthToken field, Account the mailbox.
type FlowResult struct {
	Pending  bool   `json:"pending"`
	Interval int    `json:"interval,omitempty"`
	Token    string `json:"token,omitempty"`
	Account  string `json:"account,omitempty"`
}

func (o *oauthState) start(ctx context.Context, ch Channel) (FlowStart, error) {
	client, err := clientOf(ch)
	if err != nil {
		return FlowStart{}, err
	}
	f := &flow{client: client, expires: o.now().Add(flowTTL)}
	start := FlowStart{ID: randomString(16), Kind: "code", Expires: int(flowTTL / time.Second)}
	if client.kind == oauthGoogle {
		f.verifier, f.state = randomString(32), randomString(16)
		port, _ := rand.Int(rand.Reader, big.NewInt(16383))
		f.redirect = fmt.Sprintf("http://127.0.0.1:%d/", 49152+port.Int64())
		challenge := sha256.Sum256([]byte(f.verifier))
		query := url.Values{
			"client_id":             {client.id},
			"redirect_uri":          {f.redirect},
			"response_type":         {"code"},
			"scope":                 {googleScope},
			"access_type":           {"offline"},
			"prompt":                {"consent"},
			"state":                 {f.state},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
			"code_challenge_method": {"S256"},
		}
		if user := ch.get("username"); user != "" {
			query.Set("login_hint", user)
		}
		start.URL = googleAuthURL + "?" + query.Encode()
	} else {
		answer, err := postForm(ctx, microsoftURL+"/"+client.tenant+"/oauth2/v2.0/devicecode",
			url.Values{"client_id": {client.id}, "scope": {microsoftScope}})
		if err != nil {
			return FlowStart{}, err
		}
		if answer.DeviceCode == "" || answer.UserCode == "" {
			return FlowStart{}, errors.New("the sign-in service sent no device code")
		}
		f.deviceCode, f.interval = answer.DeviceCode, max(answer.Interval, 5)
		if answer.ExpiresIn > 0 {
			f.expires = o.now().Add(time.Duration(answer.ExpiresIn) * time.Second)
			start.Expires = answer.ExpiresIn
		}
		start.Kind, start.URL, start.UserCode, start.Interval = "device", answer.VerificationURI, answer.UserCode, f.interval
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expire()
	for len(o.flows) >= maxFlows {
		oldest := ""
		for id, other := range o.flows {
			if oldest == "" || other.expires.Before(o.flows[oldest].expires) {
				oldest = id
			}
		}
		delete(o.flows, oldest)
	}
	o.flows[start.ID] = f
	return start, nil
}

// finish completes a sign-in: Google's with the code (or the whole address
// the browser ended on), Microsoft's by asking whether the user is done.
func (o *oauthState) finish(ctx context.Context, id, code string) (FlowResult, error) {
	o.mu.Lock()
	o.expire()
	f := o.flows[id]
	o.mu.Unlock()
	if f == nil {
		return FlowResult{}, errors.New("the sign-in has expired; start it again")
	}
	c := f.client
	var form url.Values
	if c.kind == oauthGoogle {
		code = strings.TrimSpace(code)
		if u, err := url.Parse(code); err == nil && (u.Query().Has("code") || u.Query().Has("error")) {
			q := u.Query()
			if e := q.Get("error"); e != "" {
				o.drop(id)
				return FlowResult{}, fmt.Errorf("Google refused the sign-in: %s", e)
			}
			if q.Get("state") != f.state {
				return FlowResult{}, errors.New("the address belongs to another sign-in")
			}
			code = q.Get("code")
		}
		if code == "" {
			return FlowResult{}, errors.New("paste the address the browser opened after the sign-in")
		}
		form = url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {c.id},
			"client_secret": {c.secret}, "redirect_uri": {f.redirect}, "code_verifier": {f.verifier}}
	} else {
		form = url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {c.id}, "device_code": {f.deviceCode}}
	}
	answer, err := postForm(ctx, c.tokenURL(), form)
	if err != nil {
		var oe oauthErr
		if errors.As(err, &oe) && c.kind == oauthMicrosoft {
			if oe.code == "authorization_pending" || oe.code == "slow_down" {
				o.mu.Lock()
				if oe.code == "slow_down" {
					f.interval += 5
				}
				interval := f.interval
				o.mu.Unlock()
				return FlowResult{Pending: true, Interval: interval}, nil
			}
		}
		o.drop(id)
		return FlowResult{}, err
	}
	o.drop(id)
	if answer.RefreshToken == "" {
		return FlowResult{}, errors.New("the sign-in service sent no refresh token; remove the app's access from the account and sign in again")
	}
	handle := pendingPrefix + randomString(24)
	o.mu.Lock()
	for len(o.pending) >= maxChannels {
		oldest := ""
		for h, p := range o.pending {
			if oldest == "" || p.expires.Before(o.pending[oldest].expires) {
				oldest = h
			}
		}
		if p := o.pending[oldest]; !p.kept {
			go revoke(p.client, p.token)
		}
		delete(o.pending, oldest)
	}
	o.pending[handle] = &pendingSignIn{client: c, token: answer.RefreshToken, expires: o.now().Add(pendingTTL)}
	o.mu.Unlock()
	return FlowResult{Token: handle, Account: accountOf(answer.IDToken)}, nil
}

func (o *oauthState) drop(id string) {
	o.mu.Lock()
	delete(o.flows, id)
	o.mu.Unlock()
}

// claim puts the refresh token of a sign-in made on the page in its place.
// The handle stays after the settings are saved (used): Test can use it
// first, and a page with edits made during the save sends it again.
func (o *oauthState) claim(ch *Channel) (string, error) {
	handle := ch.get("oauthToken")
	if !strings.HasPrefix(handle, pendingPrefix) {
		return "", nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expire()
	p := o.pending[handle]
	if p == nil {
		return "", errors.New("the sign-in has expired; sign in again")
	}
	settings := map[string]string{}
	for key, value := range ch.Settings {
		settings[key] = value
	}
	settings["oauthToken"] = p.token
	ch.Settings = settings
	return handle, nil
}

func (o *oauthState) used(handles []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, h := range handles {
		if p := o.pending[h]; p != nil {
			p.kept = true
		}
	}
}

// xoauth2 is the SASL XOAUTH2 login of Gmail and Exchange Online.
type xoauth2 struct{ user, token string }

func (a xoauth2) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("unencrypted connection")
	}
	return "XOAUTH2", []byte("user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"), nil
}

// Next answers the error details a refusal comes with by an empty line, as
// the protocol asks; the server then sends the refusal.
func (a xoauth2) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil
	}
	return nil, nil
}
