package auth

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var systemPasswordUpdater = changeRootPassword

func (s *Service) ChangePassword(c *gin.Context) {
	var req proto.ChangePasswordReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	principal, ok := middleware.CurrentPrincipal(c)
	if !ok {
		rsp.ErrRsp(c, -2, "invalid session")
		return
	}
	// The forced change follows a login with the factory password, which
	// every device shares: asking for it again proves nothing, and the new
	// session below spares a second login.
	forced := principal.MustChangePassword
	if !forced {
		currentPassword, err := utils.DecodeCredentialPassword(req.CurrentPassword, req.PasswordEncoding)
		if err != nil || currentPassword == "" {
			rsp.ErrRsp(c, -3, "current password is required")
			return
		}
		// The current password is a password check like a login: one at a
		// time per address, failures counted, the lockout applied. Otherwise
		// any session could run bcrypt back to back (about a second each on
		// the device's one core) and guess its own account's password.
		clientIP := GetClientIP(c)
		release, admitted, code, msg := BeginLoginAttempt(clientIP)
		if !admitted {
			time.Sleep(3 * time.Second)
			rsp.ErrRsp(c, code, msg)
			return
		}
		defer release()
		if _, authenticated, authErr := authn.DefaultStore.Authenticate(principal.Username, currentPassword); authErr != nil {
			rsp.ErrRsp(c, -4, "authentication unavailable")
			return
		} else if !authenticated {
			locked, code, msg := RecordLoginFailureFor(clientIP, principal.Username)
			time.Sleep(2 * time.Second)
			if locked {
				rsp.ErrRsp(c, code, msg)
				return
			}
			rsp.ErrRsp(c, -3, "current password is incorrect")
			return
		}
	}
	user, err := changeUserPassword(principal.Username, req.Password, req.PasswordEncoding)
	if err != nil {
		rsp.ErrRsp(c, -5, err.Error())
		return
	}

	// Every earlier session of this user ends (the token version changed).
	middleware.RevokeUserSessions(principal.Username)
	log.Infof("password changed for user: %s", principal.Username)
	if !forced {
		clearSessionCookie(c)
		rsp.OkRsp(c)
		return
	}
	token, err := middleware.GenerateJWT(user.Username, user.TokenVersion)
	if err != nil {
		// The password is changed; the user signs in with it.
		clearSessionCookie(c)
		rsp.OkRsp(c)
		return
	}
	c.Header("Cache-Control", "no-store")
	setSessionCookie(c, token)
	data := &proto.ChangePasswordRsp{SignedIn: true}
	if strings.EqualFold(strings.TrimSpace(c.GetHeader(ReturnTokenHeader)), "true") {
		data.Token = token
	}
	rsp.OkRspWithData(c, data)
}

func (s *Service) IsPasswordUpdated(c *gin.Context) {
	var rsp proto.Response
	if config.GetInstance().Authentication == "disable" {
		rsp.OkRspWithData(c, &proto.IsPasswordUpdatedRsp{IsUpdated: true})
		return
	}
	principal, ok := middleware.CurrentPrincipal(c)
	if !ok {
		rsp.ErrRsp(c, -1, "invalid session")
		return
	}
	user, err := authn.DefaultStore.Get(principal.Username)
	if err != nil {
		rsp.ErrRsp(c, -2, "failed to get password state")
		return
	}
	rsp.OkRspWithData(c, &proto.IsPasswordUpdatedRsp{IsUpdated: !user.MustChangePassword})
}

func changeUserPassword(username, encodedPassword, encoding string) (*authn.User, error) {
	password, err := utils.DecodeCredentialPassword(encodedPassword, encoding)
	if err != nil || password == "" {
		return nil, errInvalidPassword
	}
	if err = authn.ValidatePassword(password); err != nil {
		return nil, err
	}
	user, err := authn.DefaultStore.Get(username)
	if err != nil {
		return nil, err
	}
	if user.SystemAccount && user.Role == authn.RoleAdmin && user.Enabled {
		return authn.DefaultStore.SetPasswordAndRun(username, password, func() error {
			return systemPasswordUpdater(password)
		})
	}
	return authn.DefaultStore.SetPassword(username, password)
}

func changeRootPassword(password string) error {
	if err := passwd(password); err != nil {
		log.Errorf("failed to change root password: %s", err)
		return err
	}
	log.Debug("change root password successful")
	return nil
}

func passwd(password string) error {
	cmd := exec.Command("passwd", "root")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdin.Close() }()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	if _, err = io.WriteString(stdin, password+"\n"); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if _, err = io.WriteString(stdin, password+"\n"); err != nil {
		return err
	}
	return cmd.Wait()
}
