package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/notify"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Two-factor authentication with time-based one-time passwords (TOTP). Each
// user turns it on for their own account; then a login needs the password
// and a code from an authenticator app, or one of ten recovery codes. An
// administrator can turn it off for another account, and the console can for
// any account (nkos-totp-reset).

const (
	totpIssuer = "NanoKVM"
	// codeTOTPRequired answers a correct password of an account with a second
	// factor; the data carries the challenge for POST /api/auth/login/totp.
	codeTOTPRequired = -6

	challengeLifetime   = 5 * time.Minute
	challengeAttempts   = 5
	maxChallenges       = 256
	setupLifetime       = 10 * time.Minute
	clockPlausibleAfter = 2025
	clockErrorMessage   = "the device clock is not set; use a recovery code or set the date and time"
	invalidCodeMessage  = "invalid authentication code"
)

// now is the clock for codes; a variable so tests can fix it.
var now = time.Now

// A login that passed the password and waits for the second factor. It is
// bound to the client address and expires; a few wrong codes end it.
type loginChallenge struct {
	username     string
	tokenVersion uint64
	clientIP     string
	expires      time.Time
	attempts     int
}

var challenges = struct {
	sync.Mutex
	byID map[string]*loginChallenge
}{byID: map[string]*loginChallenge{}}

func newChallenge(user *authn.User, clientIP string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	challenges.Lock()
	defer challenges.Unlock()
	pruneChallengesLocked(now())
	if len(challenges.byID) >= maxChallenges {
		// Each one costs its requester a correct password: the oldest goes.
		var oldestID string
		var oldest time.Time
		for key, challenge := range challenges.byID {
			if oldestID == "" || challenge.expires.Before(oldest) {
				oldestID, oldest = key, challenge.expires
			}
		}
		delete(challenges.byID, oldestID)
	}
	challenges.byID[id] = &loginChallenge{
		username: user.Username, tokenVersion: user.TokenVersion, clientIP: clientIP,
		expires: now().Add(challengeLifetime),
	}
	return id, nil
}

func pruneChallengesLocked(at time.Time) {
	for id, challenge := range challenges.byID {
		if !at.Before(challenge.expires) {
			delete(challenges.byID, id)
		}
	}
}

// takeChallengeAttempt returns the challenge for one more code, or nil when
// it is unknown, expired, from another address or out of attempts.
func takeChallengeAttempt(id, clientIP string) *loginChallenge {
	challenges.Lock()
	defer challenges.Unlock()
	pruneChallengesLocked(now())
	challenge := challenges.byID[id]
	if challenge == nil || subtle.ConstantTimeCompare([]byte(challenge.clientIP), []byte(clientIP)) != 1 {
		return nil
	}
	challenge.attempts++
	if challenge.attempts >= challengeAttempts {
		delete(challenges.byID, id)
	}
	copy := *challenge
	return &copy
}

func dropChallenge(id string) {
	challenges.Lock()
	delete(challenges.byID, id)
	challenges.Unlock()
}

// clockPlausible reports whether the device clock can check codes: a device
// that booted without network may still be in 1970.
func clockPlausible() bool { return now().Year() >= clockPlausibleAfter }

// secondFactorFailed records a wrong code like a wrong password, so the
// login lockout applies to codes too.
func (s *Service) secondFactorFailed(c *gin.Context, clientIP, username string, rsp *proto.Response) {
	locked, code, msg := RecordLoginFailureFor(clientIP, username)
	time.Sleep(2 * loginPenalty)
	switch {
	case locked:
		rsp.ErrRsp(c, code, msg)
	case !clockPlausible():
		rsp.ErrRsp(c, -7, clockErrorMessage)
	default:
		rsp.ErrRsp(c, -2, invalidCodeMessage)
	}
}

// requireSecondFactor answers a correct password: with the code in the
// request it checks it, else it starts a challenge. It returns the account to
// sign in, or nil when it has answered.
func (s *Service) requireSecondFactor(c *gin.Context, clientIP string, user *authn.User, otp string, rsp *proto.Response) *authn.User {
	if otp != "" {
		verified, recovery, left, err := authn.DefaultStore.VerifySecondFactor(user.Username, otp, now())
		if err != nil {
			s.secondFactorFailed(c, clientIP, user.Username, rsp)
			return nil
		}
		logRecovery(verified.Username, recovery, left)
		return verified
	}
	id, err := newChallenge(user, clientIP)
	if err != nil {
		rsp.ErrRsp(c, -3, "authentication unavailable")
		return nil
	}
	rsp.Err(codeTOTPRequired, "authentication code required")
	rsp.Data = &proto.LoginChallengeRsp{Challenge: id}
	c.JSON(http.StatusOK, rsp)
	return nil
}

func logRecovery(username string, recovery bool, left int) {
	if recovery {
		log.Warnf("user %s signed in with a recovery code; %d left", username, left)
	}
}

// LoginTOTP finishes a login with the second factor.
func (s *Service) LoginTOTP(c *gin.Context) {
	var req proto.LoginTOTPReq
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", ReturnTokenHeader)
	clientIP := GetClientIP(c)
	if locked, code, msg := CheckLoginAttempt(clientIP); locked {
		time.Sleep(3 * loginPenalty)
		rsp.ErrRsp(c, code, msg)
		return
	}
	if err := proto.ParseFormRequest(c, &req); err != nil {
		time.Sleep(3 * loginPenalty)
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	release, admitted, code, msg := BeginLoginAttempt(clientIP)
	if !admitted {
		time.Sleep(3 * loginPenalty)
		rsp.ErrRsp(c, code, msg)
		return
	}
	defer release()
	challenge := takeChallengeAttempt(req.Challenge, clientIP)
	if challenge == nil {
		time.Sleep(2 * loginPenalty)
		rsp.ErrRsp(c, -8, "the login expired; sign in again")
		return
	}
	user, recovery, left, err := authn.DefaultStore.VerifySecondFactor(challenge.username, req.OTP, now())
	if err == nil && user.TokenVersion != challenge.tokenVersion {
		// The account changed since the password was checked (a new
		// password, a rename, sessions revoked): start again.
		dropChallenge(req.Challenge)
		rsp.ErrRsp(c, -8, "the login expired; sign in again")
		return
	}
	if err != nil {
		s.secondFactorFailed(c, clientIP, challenge.username, &rsp)
		return
	}
	dropChallenge(req.Challenge)
	logRecovery(user.Username, recovery, left)
	s.finishLogin(c, clientIP, user, &rsp)
}

// finishLogin starts the session of an authenticated user.
func (s *Service) finishLogin(c *gin.Context, clientIP string, user *authn.User, rsp *proto.Response) {
	ClearLoginAttempt(clientIP, user.Username)
	token, err := middleware.GenerateJWT(user.Username, user.TokenVersion)
	if err != nil {
		time.Sleep(loginPenalty)
		rsp.ErrRsp(c, -3, "generate token failed")
		return
	}
	setSessionCookie(c, token)
	if strings.EqualFold(strings.TrimSpace(c.GetHeader(ReturnTokenHeader)), "true") {
		rsp.OkRspWithData(c, &proto.LoginRsp{Token: token})
	} else {
		rsp.OkRsp(c)
	}
	log.Infof("user logged in: %s", user.Username)
	go notify.LoginSucceeded(user.Username, clientIP)
}

// Secrets offered by TOTPSetup until confirmed, per user.
type pendingSetup struct {
	secret  string
	expires time.Time
}

var setups = struct {
	sync.Mutex
	byUser map[string]pendingSetup
}{byUser: map[string]pendingSetup{}}

// TOTPSetup offers a new secret for the signed-in user to add to an
// authenticator app; it takes effect with TOTPEnable.
func (s *Service) TOTPSetup(c *gin.Context) {
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	principal, ok := middleware.CurrentPrincipal(c)
	if !ok {
		rsp.ErrRsp(c, -1, "invalid session")
		return
	}
	user, err := authn.DefaultStore.Get(principal.Username)
	if err != nil {
		rsp.ErrRsp(c, -1, "invalid session")
		return
	}
	if user.TOTP != nil {
		rsp.ErrRsp(c, -2, authn.ErrTOTPAlreadyEnabled.Error())
		return
	}
	secret, err := authn.NewTOTPSecret()
	if err != nil {
		rsp.ErrRsp(c, -3, "cannot create a secret")
		return
	}
	setups.Lock()
	setups.byUser[principal.Username] = pendingSetup{secret: secret, expires: now().Add(setupLifetime)}
	setups.Unlock()
	rsp.OkRspWithData(c, &proto.TOTPSetupRsp{Secret: secret, URI: authn.TOTPURI(totpIssuer, principal.Username, secret)})
}

// TOTPEnable confirms the offered secret with a code from the app, turns the
// second factor on and returns the recovery codes, shown this once.
func (s *Service) TOTPEnable(c *gin.Context) {
	var req proto.TOTPCodeReq
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	principal, ok := middleware.CurrentPrincipal(c)
	if !ok {
		rsp.ErrRsp(c, -1, "invalid session")
		return
	}
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	setups.Lock()
	setup, found := setups.byUser[principal.Username]
	setups.Unlock()
	if !found || !now().Before(setup.expires) {
		rsp.ErrRsp(c, -2, "start the setup again")
		return
	}
	step := authn.CheckTOTPSetup(setup.secret, req.OTP, now())
	if step == 0 {
		time.Sleep(loginPenalty)
		if !clockPlausible() {
			rsp.ErrRsp(c, -7, clockErrorMessage)
			return
		}
		rsp.ErrRsp(c, -3, invalidCodeMessage)
		return
	}
	codes, hashes, err := authn.NewRecoveryCodes()
	if err != nil {
		rsp.ErrRsp(c, -4, "cannot create recovery codes")
		return
	}
	if err = authn.DefaultStore.EnableTOTP(principal.Username, setup.secret, step, hashes); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	setups.Lock()
	delete(setups.byUser, principal.Username)
	setups.Unlock()
	log.Infof("two-factor authentication enabled for user: %s", principal.Username)
	rsp.OkRspWithData(c, &proto.RecoveryCodesRsp{RecoveryCodes: codes})
}

// checkPasswordAndCode proves the signed-in user's password and second factor,
// counted like a login. It answers and returns "" on failure.
func (s *Service) checkPasswordAndCode(c *gin.Context, rsp *proto.Response) string {
	var req proto.TOTPCheckReq
	principal, ok := middleware.CurrentPrincipal(c)
	if !ok {
		rsp.ErrRsp(c, -1, "invalid session")
		return ""
	}
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return ""
	}
	password, err := utils.DecodeCredentialPassword(req.Password, req.PasswordEncoding)
	if err != nil || password == "" {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return ""
	}
	clientIP := GetClientIP(c)
	release, admitted, code, msg := BeginLoginAttempt(clientIP)
	if !admitted {
		time.Sleep(3 * loginPenalty)
		rsp.ErrRsp(c, code, msg)
		return ""
	}
	defer release()
	if _, authenticated, err := authn.DefaultStore.Authenticate(principal.Username, password); err != nil {
		rsp.ErrRsp(c, -4, "authentication unavailable")
		return ""
	} else if !authenticated {
		locked, code, msg := RecordLoginFailureFor(clientIP, principal.Username)
		time.Sleep(2 * loginPenalty)
		if locked {
			rsp.ErrRsp(c, code, msg)
		} else {
			rsp.ErrRsp(c, -3, "current password is incorrect")
		}
		return ""
	}
	_, recovery, left, err := authn.DefaultStore.VerifySecondFactor(principal.Username, req.OTP, now())
	if errors.Is(err, authn.ErrTOTPNotEnabled) {
		rsp.ErrRsp(c, -5, err.Error())
		return ""
	}
	if err != nil {
		s.secondFactorFailed(c, clientIP, principal.Username, rsp)
		return ""
	}
	logRecovery(principal.Username, recovery, left)
	return principal.Username
}

// TOTPDisable turns the user's own second factor off.
func (s *Service) TOTPDisable(c *gin.Context) {
	var rsp proto.Response
	username := s.checkPasswordAndCode(c, &rsp)
	if username == "" {
		return
	}
	if _, err := authn.DefaultStore.DisableTOTP(username); err != nil {
		rsp.ErrRsp(c, -6, err.Error())
		return
	}
	log.Infof("two-factor authentication disabled by user: %s", username)
	rsp.OkRsp(c)
}

// TOTPRecoveryCodes replaces the user's recovery codes.
func (s *Service) TOTPRecoveryCodes(c *gin.Context) {
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	username := s.checkPasswordAndCode(c, &rsp)
	if username == "" {
		return
	}
	codes, hashes, err := authn.NewRecoveryCodes()
	if err == nil {
		err = authn.DefaultStore.ReplaceRecoveryCodes(username, hashes)
	}
	if err != nil {
		rsp.ErrRsp(c, -6, err.Error())
		return
	}
	log.Infof("recovery codes replaced for user: %s", username)
	rsp.OkRspWithData(c, &proto.RecoveryCodesRsp{RecoveryCodes: codes})
}

// ResetUserTOTP turns another user's second factor off (administrators). As
// with passwords, the device owner's stays the owner's to change; the console
// can reset it.
func (s *Service) ResetUserTOTP(c *gin.Context) {
	var rsp proto.Response
	principal, _ := middleware.CurrentPrincipal(c)
	username := c.Param("username")
	if username == principal.Username {
		rsp.ErrRsp(c, -1, "turn off your own two-factor authentication in your account")
		return
	}
	user, err := authn.DefaultStore.Get(username)
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	if user.SystemAccount {
		rsp.ErrRsp(c, -3, "the device owner must turn off its own two-factor authentication")
		return
	}
	was, err := authn.DefaultStore.DisableTOTP(username)
	if err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	if was {
		log.Infof("two-factor authentication reset for user %s by %s", username, principal.Username)
	}
	rsp.OkRsp(c)
}
