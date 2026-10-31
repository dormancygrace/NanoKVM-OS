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

// maxAddresses per user; the oldest is forgotten first.
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

// LoginSucceeded reports a login from an address the user has not logged in
// from before. A user's first login is remembered without a report: the
// list starts when someone wants these events.
func LoginSucceeded(user, address string) {
	if address == "" || !Wants(KindSecurity) {
		return
	}
	known, err := rememberAddress(AddressesPath, user, address)
	if err != nil {
		log.Warnf("notifications: login addresses: %v", err)
		return
	}
	if !known {
		Emit(Event{Kind: KindSecurity, Severity: SeverityWarning, Message: "security.new-address",
			Vars: map[string]string{"user": user, "address": address}})
	}
}

// rememberAddress adds address to the user's list and reports whether it was
// there already; a user with no list yet counts as known.
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
	if slices.Contains(list, address) {
		return true, nil
	}
	list = append(list, address)
	if len(list) > maxAddresses {
		list = list[len(list)-maxAddresses:]
	}
	lists[user] = list
	data, err = json.Marshal(lists)
	if err != nil {
		return true, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return true, err
	}
	return !seen, atomicfile.Write(path, data, 0o600)
}
