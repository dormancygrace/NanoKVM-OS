package notify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/atomicfile"
)

// AddressesPath keeps the addresses each user has logged in from, for the
// "login from a new address" event.
const AddressesPath = "/etc/kvm/notify/addresses.json"

// maxAddresses per user; one not used for long is forgotten first.
const maxAddresses = 32

var addressesMu sync.Mutex

// Wants reports whether an enabled channel takes events of this kind, so a
// producer can skip work nobody would hear about.
func (s *Service) Wants(kind Kind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.config.Channels {
		if ch.Enabled && ch.wants(kind) {
			return true
		}
	}
	return false
}

// Wants is Service.Wants of the device's service.
func Wants(kind Kind) bool { return Default().Wants(kind) }

// LoginLockedOut reports the lockout of an address after failures failed
// logins; logins from it are refused for minutes.
func LoginLockedOut(address string, failures, minutes int) {
	Emit(Event{Kind: KindSecurity, Severity: SeverityWarning, Message: "security.lockout",
		Vars: map[string]string{"failures": strconv.Itoa(failures), "address": address, "minutes": strconv.Itoa(minutes)}})
}

// LoginAttemptsLimited reports that an address made attempts logins, failed
// or not, and further ones are refused for minutes: the IPMI limit, which
// counts every login because each hands out a password hash.
func LoginAttemptsLimited(address string, attempts, minutes int) {
	Emit(Event{Kind: KindSecurity, Severity: SeverityWarning, Message: "security.attempt-limit",
		Vars: map[string]string{"attempts": strconv.Itoa(attempts), "address": address, "minutes": strconv.Itoa(minutes)}})
}

// LoginSucceeded reports a login from an address the user has not logged in
// from before. A user's first login is remembered without a report. The
// addresses are kept also while no channel wants these events, so turning
// them on later does not trust the next login, which may be an attacker's.
func LoginSucceeded(user, address string) {
	if address == "" {
		return
	}
	known, err := rememberAddress(AddressesPath, user, address)
	if err != nil {
		// A new address is still reported when it could not be stored.
		log.Warnf("notifications: login addresses: %v", err)
	}
	if !known {
		Emit(Event{Kind: KindSecurity, Severity: SeverityWarning, Message: "security.new-address",
			Vars: map[string]string{"user": user, "address": address}})
	}
}

// rememberAddress adds address to the user's list and reports whether it was
// there already, also when the list could not be stored; a user with no list
// yet counts as known.
func rememberAddress(path, user, address string) (bool, error) {
	addressesMu.Lock()
	defer addressesMu.Unlock()
	lists := map[string][]string{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &lists); err != nil {
			log.Warnf("notifications: %s unreadable, starting afresh", path)
			lists = map[string][]string{}
		}
	}
	list, seen := lists[user]
	known := false
	if i := slices.Index(list, address); i >= 0 {
		// An address in use moves to the newest end, so it is not forgotten
		// before those left behind. Only one in the older half moves, so
		// logins switching between a few addresses do not write each time.
		if i >= len(list)-maxAddresses/2 {
			return true, nil
		}
		list = slices.Delete(list, i, i+1)
		known = true
	}
	list = append(list, address)
	if len(list) > maxAddresses {
		list = list[len(list)-maxAddresses:]
	}
	lists[user] = list
	known = known || !seen
	data, err = json.Marshal(lists)
	if err != nil {
		return known, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return known, err
	}
	return known, atomicfile.Write(path, data, 0o600)
}
