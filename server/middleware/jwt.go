package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	log "github.com/sirupsen/logrus"
)

const (
	principalContextKey = "principal"
	tokenContextKey     = "token"
	CookieName          = "nano-kvm-token"
	sessionRecheckDelay = 5 * time.Second
)

type Principal struct {
	Username           string
	Role               authn.Role
	MustChangePassword bool
}

// PasswordChangeRequiredCode is returned (HTTP 403) while the account still
// uses its factory password. Only the routes in passwordChangeRoutes work.
const PasswordChangeRequiredCode = -10

var passwordChangeRoutes = map[string]bool{
	http.MethodGet + " /api/auth/account":   true,
	http.MethodGet + " /api/auth/password":  true,
	http.MethodPost + " /api/auth/password": true,
	http.MethodPost + " /api/auth/logout":   true,
	http.MethodGet + " /api/vm/web-title":   true,
}

func requiresPasswordChange(c *gin.Context, principal Principal) bool {
	if !principal.MustChangePassword || passwordChangeRoutes[c.Request.Method+" "+c.Request.URL.Path] {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{"code": PasswordChangeRequiredCode, "msg": "password change required"})
	c.Abort()
	return true
}

type Token struct {
	Username     string `json:"username"`
	TokenVersion uint64 `json:"tokenVersion"`
	jwt.RegisteredClaims
}

func CheckToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, token, ok := authenticate(c)
		if !ok {
			abortUnauthorized(c)
			return
		}
		if requiresPasswordChange(c, principal) {
			return
		}

		c.Set(principalContextKey, principal)
		c.Set(tokenContextKey, token)
		if token == nil || token.ExpiresAt == nil {
			c.Next()
			return
		}

		requestContext, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(requestContext)
		unregister := activeSessions.register(principal.Username, cancel)
		timer := time.AfterFunc(time.Until(token.ExpiresAt.Time), cancel)
		go watchSessionState(requestContext, cancel, principal.Username, token.TokenVersion, sessionRecheckDelay)
		defer func() {
			timer.Stop()
			unregister()
			cancel()
		}()

		c.Next()
	}
}

func watchSessionState(ctx context.Context, cancel context.CancelFunc, username string, tokenVersion uint64, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := authn.DefaultStore.ValidateToken(username, tokenVersion); err != nil {
				cancel()
				return
			}
		}
	}
}

func RequireRole(roles ...authn.Role) gin.HandlerFunc {
	allowed := make(map[authn.Role]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(c *gin.Context) {
		principal, ok := CurrentPrincipal(c)
		if !ok {
			abortUnauthorized(c)
			return
		}
		if _, ok = allowed[principal.Role]; !ok {
			c.JSON(http.StatusForbidden, "forbidden")
			c.Abort()
			return
		}
		c.Next()
	}
}

// CurrentSessionID identifies the login session behind a request, so HTTP
// input requests can be matched with the WebSocket that owns input control.
// Requests sharing one login (tabs of one browser) share an ID. It is empty
// when authentication is disabled.
func CurrentSessionID(c *gin.Context) string {
	value, exists := c.Get(tokenContextKey)
	if !exists {
		return ""
	}
	token, ok := value.(*Token)
	if !ok || token == nil {
		return ""
	}
	var issued int64
	if token.IssuedAt != nil {
		issued = token.IssuedAt.UnixNano()
	}
	return fmt.Sprintf("%s/%d/%d", token.Username, token.TokenVersion, issued)
}

func CurrentPrincipal(c *gin.Context) (Principal, bool) {
	value, exists := c.Get(principalContextKey)
	if !exists {
		return Principal{}, false
	}
	principal, ok := value.(Principal)
	return principal, ok
}

func CheckLoopbackInternalToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if allowByLoopbackInternalToken(c.Request) {
			c.Next()
			return
		}
		abortUnauthorized(c)
	}
}

func CheckTokenOrLoopbackInternalToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if allowByLoopbackInternalToken(c.Request) {
			c.Next()
			return
		}

		principal, token, ok := authenticate(c)
		if !ok {
			abortUnauthorized(c)
			return
		}
		if requiresPasswordChange(c, principal) {
			return
		}
		c.Set(principalContextKey, principal)
		c.Set(tokenContextKey, token)
		c.Next()
	}
}

func authenticate(c *gin.Context) (Principal, *Token, bool) {
	conf := config.GetInstance()
	if conf.Authentication == "disable" {
		return Principal{Username: "admin", Role: authn.RoleAdmin}, nil, true
	}

	rawToken, supplied := bearerToken(c.GetHeader("Authorization"))
	if !supplied {
		var err error
		rawToken, err = c.Cookie(CookieName)
		if err != nil {
			return Principal{}, nil, false
		}
	}
	if rawToken == "" {
		return Principal{}, nil, false
	}
	token, err := ParseJWT(rawToken)
	if err != nil {
		return Principal{}, nil, false
	}
	user, err := authn.DefaultStore.ValidateToken(token.Username, token.TokenVersion)
	if err != nil {
		log.Debugf("validate session for %q: %s", token.Username, err)
		return Principal{}, nil, false
	}
	return Principal{Username: user.Username, Role: user.Role, MustChangePassword: user.MustChangePassword}, token, true
}

// bearerToken reports whether the caller supplied an Authorization header.
// A supplied but malformed Bearer credential must not fall back to a cookie:
// explicit automation credentials take precedence over ambient browser state.
func bearerToken(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", true
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", true
	}
	return token, true
}

func abortUnauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, "unauthorized")
	c.Abort()
}

func GenerateJWT(username string, tokenVersion uint64) (string, error) {
	conf := config.GetInstance()
	now := time.Now()
	expireDuration := time.Duration(conf.JWT.RefreshTokenDuration) * time.Second
	claims := Token{
		Username:     username,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expireDuration)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(conf.JWT.SecretKey))
}

func ParseJWT(jwtToken string) (*Token, error) {
	conf := config.GetInstance()
	parsed, err := jwt.ParseWithClaims(
		jwtToken,
		&Token{},
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(conf.JWT.SecretKey), nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		log.Debugf("parse jwt error: %s", err)
		return nil, err
	}
	claims, ok := parsed.Claims.(*Token)
	if !ok || !parsed.Valid || claims.Username == "" || claims.Subject != claims.Username || claims.TokenVersion == 0 {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}
