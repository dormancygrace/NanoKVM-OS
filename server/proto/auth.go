package proto

type LoginReq struct {
	// Empty selects the legacy CryptoJS format for existing clients.
	PasswordEncoding string `json:"passwordEncoding" form:"passwordEncoding"`
	Username         string `json:"username" form:"username" validate:"required"`
	Password         string `json:"password" form:"password" validate:"required"`
	// OTP is the authenticator or recovery code of an account with two-factor
	// authentication, for clients that sign in with one request. Without it
	// such an account gets a LoginChallengeRsp (code -6).
	OTP string `json:"otp" form:"otp"`
}

// LoginChallengeRsp is the data of a login that needs the second factor
// (code -6): POST /api/auth/login/totp finishes it within five minutes.
type LoginChallengeRsp struct {
	Challenge string `json:"challenge"`
}

type LoginTOTPReq struct {
	Challenge string `json:"challenge" form:"challenge" validate:"required"`
	OTP       string `json:"otp" form:"otp" validate:"required"`
}

// TOTPSetupRsp is a new secret to confirm with POST /api/auth/totp/enable.
type TOTPSetupRsp struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

type TOTPCodeReq struct {
	OTP string `json:"otp" form:"otp" validate:"required"`
}

// TOTPCheckReq proves the account's password and second factor, for turning
// it off or replacing the recovery codes.
type TOTPCheckReq struct {
	// Empty selects the legacy CryptoJS format for existing clients.
	PasswordEncoding string `json:"passwordEncoding" form:"passwordEncoding"`
	Password         string `json:"password" form:"password" validate:"required"`
	OTP              string `json:"otp" form:"otp" validate:"required"`
}

// RecoveryCodesRsp shows recovery codes once.
type RecoveryCodesRsp struct {
	RecoveryCodes []string `json:"recoveryCodes"`
}

// LoginRsp is returned only when the client explicitly requests an automation
// token with the X-NanoKVM-Return-Token header. Browser logins use the
// HttpOnly session cookie and receive the normal empty success response.
type LoginRsp struct {
	Token string `json:"token"`
}

type GetAccountRsp struct {
	Username           string `json:"username"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"mustChangePassword"`
	TOTPEnabled        bool   `json:"totpEnabled"`
	// RecoveryCodesLeft is how many recovery codes remain, with TOTPEnabled.
	RecoveryCodesLeft int `json:"recoveryCodesLeft,omitempty"`
}

type ChangePasswordReq struct {
	// Empty selects the legacy CryptoJS format for existing clients.
	PasswordEncoding string `json:"passwordEncoding" form:"passwordEncoding"`
	CurrentPassword  string `json:"currentPassword" form:"currentPassword"`
	Password         string `json:"password" form:"password" validate:"required"`
}

// ChangePasswordRsp answers a forced change of the factory password: the
// change signs the browser in with a new session cookie (Token only with the
// X-NanoKVM-Return-Token header). Other changes end every session instead
// and return no data.
type ChangePasswordRsp struct {
	SignedIn bool   `json:"signedIn"`
	Token    string `json:"token,omitempty"`
}

type IsPasswordUpdatedRsp struct {
	IsUpdated bool `json:"isUpdated"`
}

type UserInfo struct {
	Username      string `json:"username"`
	Role          string `json:"role"`
	Enabled       bool   `json:"enabled"`
	SystemAccount bool   `json:"systemAccount,omitempty"`
	TOTPEnabled   bool   `json:"totpEnabled,omitempty"`
}

type ListUsersRsp struct {
	Users []UserInfo `json:"users"`
}

type CreateUserReq struct {
	// Empty selects the legacy CryptoJS format for existing clients.
	PasswordEncoding string `json:"passwordEncoding" form:"passwordEncoding"`
	Username         string `json:"username" form:"username" validate:"required"`
	Password         string `json:"password" form:"password" validate:"required"`
	Role             string `json:"role" form:"role" validate:"required"`
}

type UpdateUserReq struct {
	Username *string `json:"username" form:"username"`
	Role     *string `json:"role" form:"role"`
	Enabled  *bool   `json:"enabled" form:"enabled"`
}
