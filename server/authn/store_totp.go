package authn

import (
	"time"
)

// updateTOTP loads the accounts, lets change edit one user's record and
// saves the file. change returns false to leave the file as it is.
func (s *Store) updateTOTP(username string, change func(*User) (bool, error)) (*User, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	db, err := s.loadLocked(true)
	if err != nil {
		return nil, err
	}
	index := userIndex(db.Users, username)
	if index < 0 {
		return nil, ErrUserNotFound
	}
	save, err := change(&db.Users[index])
	if err != nil {
		return nil, err
	}
	if save {
		if err = s.saveLocked(db); err != nil {
			return nil, err
		}
	}
	return cloneUser(&db.Users[index]), nil
}

// EnableTOTP turns the second factor on with a confirmed secret and the
// hashes of fresh recovery codes.
func (s *Store) EnableTOTP(username, secret string, confirmedStep int64, recoveryHashes []string) error {
	_, err := s.updateTOTP(username, func(user *User) (bool, error) {
		if user.TOTP != nil {
			return false, ErrTOTPAlreadyEnabled
		}
		totp := &TOTP{Secret: secret, RecoveryHashes: append([]string(nil), recoveryHashes...)}
		if !totp.valid() {
			return false, ErrTOTPInvalidCode
		}
		// The code that confirmed the secret must not sign in again.
		totp.LastStep = confirmedStep
		user.TOTP = totp
		return true, nil
	})
	return err
}

// DisableTOTP turns the second factor off: by the user after a check, by an
// administrator, or from the console (nkos-totp-reset). It reports whether
// it was on.
func (s *Store) DisableTOTP(username string) (bool, error) {
	was := false
	_, err := s.updateTOTP(username, func(user *User) (bool, error) {
		was = user.TOTP != nil
		user.TOTP = nil
		return was, nil
	})
	return was, err
}

// ReplaceRecoveryCodes stores the hashes of a new set of recovery codes; the
// old codes stop working.
func (s *Store) ReplaceRecoveryCodes(username string, recoveryHashes []string) error {
	_, err := s.updateTOTP(username, func(user *User) (bool, error) {
		if user.TOTP == nil {
			return false, ErrTOTPNotEnabled
		}
		user.TOTP.RecoveryHashes = append([]string(nil), recoveryHashes...)
		return true, nil
	})
	return err
}

// VerifySecondFactor checks an authenticator code, or a recovery code, for an
// enabled account and consumes it: a code's time step, or the recovery code,
// is not accepted again. It returns the account as saved, whether a recovery
// code was used and how many remain.
func (s *Store) VerifySecondFactor(username, code string, now time.Time) (*User, bool, int, error) {
	usedRecovery := false
	user, err := s.updateTOTP(username, func(user *User) (bool, error) {
		if !user.Enabled {
			return false, ErrTOTPInvalidCode
		}
		if user.TOTP == nil {
			return false, ErrTOTPNotEnabled
		}
		if step := matchTOTP(user.TOTP.Secret, code, now, user.TOTP.LastStep); step > 0 {
			user.TOTP.LastStep = step
			return true, nil
		}
		if index := matchRecovery(user.TOTP.RecoveryHashes, code); index >= 0 {
			hashes := user.TOTP.RecoveryHashes
			user.TOTP.RecoveryHashes = append(append([]string(nil), hashes[:index]...), hashes[index+1:]...)
			usedRecovery = true
			return true, nil
		}
		return false, ErrTOTPInvalidCode
	})
	if err != nil {
		return nil, false, 0, err
	}
	return user, usedRecovery, len(user.TOTP.RecoveryHashes), nil
}

// CheckTOTPSetup returns the step of the first code of a secret that is not
// saved yet, 0 when the code is wrong.
func CheckTOTPSetup(secret, code string, now time.Time) int64 {
	return matchTOTP(secret, code, now, 0)
}
