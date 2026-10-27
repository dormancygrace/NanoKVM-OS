// Package redfish serves the part of DMTF Redfish that power management
// tools use (fence_redfish, Ansible's community.general.redfish_*, Metal3):
// the service root, one computer system with its power state and reset
// action, its chassis and manager, and login sessions.
package redfish

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/pkg/bmc"
)

const (
	root            = "/redfish/v1"
	systemPath      = root + "/Systems/1"
	chassisPath     = root + "/Chassis/1"
	managerPath     = root + "/Managers/1"
	sessionsPath    = root + "/SessionService/Sessions"
	resetActionPath = systemPath + "/Actions/ComputerSystem.Reset"

	maxSessions    = 8
	sessionTimeout = 30 * time.Minute
	// After this many failed logins from one client within failWindow, its
	// logins are refused for failWindow.
	maxFailures = 5
	failWindow  = time.Minute
)

// Info describes the device in the resources.
type Info struct {
	UUID            string // RFC 4122 form
	FirmwareVersion string // NanoKVM OS release
	Hostname        string
}

// Power is what the handler needs from bmc.Power.
type Power interface {
	Status(ctx context.Context) (on, known bool, err error)
	Available(ctx context.Context) (power, reset bool, err error)
	On(ctx context.Context) error
	ForceOff(ctx context.Context) error
	Soft(ctx context.Context) error
	Push(ctx context.Context) error
	Reset(ctx context.Context) error
	Cycle(ctx context.Context) error
}

type session struct {
	id, token string
	lastUsed  time.Time
}

type failures struct {
	count int
	since time.Time
}

// Handler is the Redfish service.
type Handler struct {
	username, password string
	info               Info
	power              Power
	now                func() time.Time

	mu       sync.Mutex
	sessions map[string]*session // by token
	failed   map[string]*failures
}

func New(username, password string, info Info, power Power) *Handler {
	return &Handler{
		username: username, password: password, info: info, power: power, now: time.Now,
		sessions: map[string]*session{}, failed: map[string]*failures{},
	}
}

type object = map[string]any

func link(path string) object { return object{"@odata.id": path} }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("OData-Version", "4.0")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError answers in the Redfish error format (Base message registry).
func writeError(w http.ResponseWriter, status int, id, message string) {
	writeJSON(w, status, object{"error": object{
		"code":    "Base.1.15." + id,
		"message": message,
		"@Message.ExtendedInfo": []object{{
			"@odata.type": "#Message.v1_1_1.Message",
			"MessageId":   "Base.1.15." + id,
			"Message":     message,
			"Severity":    "Warning",
		}},
	}})
}

// client is the requester's address, from the connection: the web server is
// the edge, and an X-Forwarded-For header is the client's own claim.
// IPv6 clients count per /64, as for the web login: one host has a whole
// prefix to rotate through.
func client(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() && !addr.Is4In6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return host
}

// beginLogin reserves one login attempt of addr. It refuses while addr is
// locked out; otherwise it counts the attempt before the credentials are
// compared, in the same critical section as the check, so parallel requests
// cannot all pass the check before the first failure is recorded. A
// successful login hands the attempt back with loginSucceeded.
func (h *Handler) beginLogin(addr string) *failures {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.failed[addr]
	if f != nil && h.now().Sub(f.since) > failWindow {
		delete(h.failed, addr)
		f = nil
	}
	if f != nil && f.count >= maxFailures {
		return nil
	}
	if f == nil {
		h.expire()
		f = &failures{since: h.now()}
		h.failed[addr] = f
	}
	f.count++
	return f
}

func (h *Handler) loginSucceeded(addr string, reservation *failures) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// A delayed success must not refund a newer window or a replacement
	// record after eviction. Only return the attempt that we reserved.
	if f := h.failed[addr]; f != nil && f == reservation {
		// A counter left at zero would take a slot of the bounded table.
		if f.count--; f.count <= 0 {
			delete(h.failed, addr)
		}
	}
}

func (h *Handler) lockedOut(addr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.failed[addr]
	if f == nil {
		return false
	}
	if h.now().Sub(f.since) > failWindow {
		delete(h.failed, addr)
		return false
	}
	return f.count >= maxFailures
}

// maxTracked bounds the login counters: a client that never comes back
// leaves its entry, and IPv6 gives a client many addresses.
const maxTracked = 1024

// expire drops the expired counters, then the oldest ones beyond room for a
// new one. h.mu is held.
func (h *Handler) expire() {
	for addr, f := range h.failed {
		if h.now().Sub(f.since) > failWindow {
			delete(h.failed, addr)
		}
	}
	for len(h.failed) >= maxTracked {
		var oldest string
		var since time.Time
		for addr, f := range h.failed {
			if oldest == "" || f.since.Before(since) {
				oldest, since = addr, f.since
			}
		}
		delete(h.failed, oldest)
	}
}

func (h *Handler) credentialsMatch(user, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(h.password)) == 1
	return userOK && passOK && h.password != ""
}

// authenticate accepts HTTP Basic or an X-Auth-Token of a session.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) bool {
	addr := client(r)
	if token := r.Header.Get("X-Auth-Token"); token != "" {
		h.mu.Lock()
		s := h.sessions[token]
		if s != nil && h.now().Sub(s.lastUsed) > sessionTimeout {
			delete(h.sessions, token)
			s = nil
		}
		if s != nil {
			s.lastUsed = h.now()
		}
		h.mu.Unlock()
		if s != nil {
			return true
		}
	} else if user, password, ok := r.BasicAuth(); ok {
		reservation := h.beginLogin(addr)
		if reservation == nil {
			writeError(w, http.StatusTooManyRequests, "GeneralError", "Too many failed logins; try again later.")
			return false
		}
		if h.credentialsMatch(user, password) {
			h.loginSucceeded(addr, reservation)
			return true
		}
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="NanoKVM BMC"`)
	writeError(w, http.StatusUnauthorized, "NoValidSession", "Authentication is required.")
	return false
}

// refuseWrite answers state-changing requests that a browser could send
// from another site with credentials it has cached for the device (Basic
// auth): 403 when the browser marks the request cross-site, 415 for a POST
// or PATCH without a JSON body, which a cross-origin form can send without a
// CORS preflight. Redfish clients (fence_redfish, Ansible, gophercloud) send
// JSON. It reports whether it answered.
func refuseWrite(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		writeError(w, http.StatusForbidden, "InsufficientPrivilege", "Requests from another site are refused.")
		return true
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		return false
	}
	if media, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); !strings.EqualFold(strings.TrimSpace(media), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "GeneralError", "The request body must be application/json.")
		return true
	}
	return false
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if refuseWrite(w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch path {
	case "/redfish":
		h.get(w, r, func() any { return object{"v1": root + "/"} })
		return
	case root:
		h.get(w, r, h.serviceRoot)
		return
	case root + "/odata":
		h.get(w, r, h.odata)
		return
	case sessionsPath:
		if r.Method == http.MethodPost {
			h.createSession(w, r)
			return
		}
	}
	if !h.authenticate(w, r) {
		return
	}
	switch {
	case path == root+"/Systems":
		h.get(w, r, func() any {
			return collection(root+"/Systems", "#ComputerSystemCollection.ComputerSystemCollection", "Computer System Collection", systemPath)
		})
	case path == systemPath:
		h.get(w, r, func() any { return h.system(r.Context()) })
	case path == resetActionPath:
		h.reset(w, r)
	case path == root+"/Chassis":
		h.get(w, r, func() any {
			return collection(root+"/Chassis", "#ChassisCollection.ChassisCollection", "Chassis Collection", chassisPath)
		})
	case path == chassisPath:
		h.get(w, r, func() any { return h.chassis(r.Context()) })
	case path == root+"/Managers":
		h.get(w, r, func() any {
			return collection(root+"/Managers", "#ManagerCollection.ManagerCollection", "Manager Collection", managerPath)
		})
	case path == managerPath:
		h.get(w, r, h.manager)
	case path == root+"/SessionService":
		h.get(w, r, h.sessionService)
	case path == sessionsPath:
		h.get(w, r, h.sessionCollection)
	case strings.HasPrefix(path, sessionsPath+"/"):
		h.session(w, r, strings.TrimPrefix(path, sessionsPath+"/"))
	default:
		writeError(w, http.StatusNotFound, "ResourceMissingAtURI", "The resource at the URI "+r.URL.Path+" was not found.")
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, body func() any) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "OperationNotAllowed", "The operation is not allowed on this resource.")
		return
	}
	writeJSON(w, http.StatusOK, body())
}

func collection(path, odataType, name string, members ...string) object {
	list := make([]object, 0, len(members))
	for _, m := range members {
		list = append(list, link(m))
	}
	return object{
		"@odata.id": path, "@odata.type": odataType, "Name": name,
		"Members": list, "Members@odata.count": len(list),
	}
}

func (h *Handler) serviceRoot() any {
	return object{
		"@odata.id":      root + "/",
		"@odata.type":    "#ServiceRoot.v1_15_0.ServiceRoot",
		"Id":             "RootService",
		"Name":           "NanoKVM Redfish Service",
		"RedfishVersion": "1.15.0",
		"UUID":           h.info.UUID,
		"Systems":        link(root + "/Systems"),
		"Chassis":        link(root + "/Chassis"),
		"Managers":       link(root + "/Managers"),
		"SessionService": link(root + "/SessionService"),
		"Links":          object{"Sessions": link(sessionsPath)},
	}
}

func (h *Handler) odata() any {
	services := []object{}
	for _, name := range []string{"Systems", "Chassis", "Managers", "SessionService"} {
		services = append(services, object{"name": name, "kind": "Singleton", "url": root + "/" + name})
	}
	return object{"@odata.context": root + "/$metadata", "value": services}
}

func powerState(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// resetTypes lists the ResetType values this board can carry out.
func (h *Handler) resetTypes(ctx context.Context) []string {
	power, reset, err := h.power.Available(ctx)
	if err != nil || !power {
		return []string{}
	}
	types := []string{"On", "ForceOff", "GracefulShutdown", "PowerCycle", "PushPowerButton"}
	if reset {
		types = append(types, "ForceRestart")
	}
	return types
}

func (h *Handler) system(ctx context.Context) any {
	system := object{
		"@odata.id":   systemPath,
		"@odata.type": "#ComputerSystem.v1_20_0.ComputerSystem",
		"Id":          "1",
		"Name":        "Host",
		"SystemType":  "Physical",
		"Description": "The computer connected to the NanoKVM",
		"Status":      object{"State": "Enabled", "Health": "OK"},
		"Actions": object{"#ComputerSystem.Reset": object{
			"target":                            resetActionPath,
			"ResetType@Redfish.AllowableValues": h.resetTypes(ctx),
		}},
		"Links": object{"Chassis": []object{link(chassisPath)}, "ManagedBy": []object{link(managerPath)}},
	}
	// Without a power LED the state is not known; Redfish has no "unknown"
	// value, so the property is left out.
	if on, known, err := h.power.Status(ctx); err == nil && known {
		system["PowerState"] = powerState(on)
	}
	return system
}

func (h *Handler) chassis(ctx context.Context) any {
	chassis := object{
		"@odata.id":   chassisPath,
		"@odata.type": "#Chassis.v1_23_0.Chassis",
		"Id":          "1",
		"Name":        "Host chassis",
		"ChassisType": "Other",
		"Status":      object{"State": "Enabled", "Health": "OK"},
		"Links": object{
			"ComputerSystems": []object{link(systemPath)},
			"ManagedBy":       []object{link(managerPath)},
		},
	}
	if on, known, err := h.power.Status(ctx); err == nil && known {
		chassis["PowerState"] = powerState(on)
	}
	return chassis
}

func (h *Handler) manager() any {
	return object{
		"@odata.id":       managerPath,
		"@odata.type":     "#Manager.v1_18_0.Manager",
		"Id":              "1",
		"Name":            "NanoKVM",
		"ManagerType":     "BMC",
		"Model":           "NanoKVM",
		"FirmwareVersion": h.info.FirmwareVersion,
		"UUID":            h.info.UUID,
		"Status":          object{"State": "Enabled", "Health": "OK"},
		"Links": object{
			"ManagerForServers": []object{link(systemPath)},
			"ManagerForChassis": []object{link(chassisPath)},
		},
	}
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "OperationNotAllowed", "Use POST for actions.")
		return
	}
	var body struct {
		ResetType string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "The request body is not valid JSON.")
		return
	}
	if body.ResetType == "" {
		// Redfish: an action without a ResetType uses the default, On.
		body.ResetType = "On"
	}
	actions := map[string]func(context.Context) error{
		"On":               h.power.On,
		"ForceOn":          h.power.On,
		"ForceOff":         h.power.ForceOff,
		"GracefulShutdown": h.power.Soft,
		"ForceRestart":     h.power.Reset,
		"PowerCycle":       h.power.Cycle,
		"PushPowerButton":  h.power.Push,
	}
	action := actions[body.ResetType]
	if action == nil {
		writeError(w, http.StatusBadRequest, "ActionParameterNotSupported",
			"The value "+body.ResetType+" for ResetType is not supported.")
		return
	}
	// The button presses finish even if the client disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	switch err := action(ctx); {
	case errors.Is(err, bmc.ErrActionBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "ServiceTemporarilyUnavailable", err.Error())
	case errors.Is(err, bmc.ErrNoATX), errors.Is(err, bmc.ErrNoReset):
		writeError(w, http.StatusBadRequest, "ActionNotSupported", err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, "GeneralError", "The power action failed.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) sessionService() any {
	return object{
		"@odata.id":      root + "/SessionService",
		"@odata.type":    "#SessionService.v1_1_9.SessionService",
		"Id":             "SessionService",
		"Name":           "Session Service",
		"ServiceEnabled": true,
		"SessionTimeout": int(sessionTimeout / time.Second),
		"Sessions":       link(sessionsPath),
	}
}

func (h *Handler) sessionCollection() any {
	h.mu.Lock()
	ids := make([]string, 0, len(h.sessions))
	for _, s := range h.sessions {
		ids = append(ids, sessionsPath+"/"+s.id)
	}
	h.mu.Unlock()
	return collection(sessionsPath, "#SessionCollection.SessionCollection", "Session Collection", ids...)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	addr := client(r)
	if h.lockedOut(addr) {
		writeError(w, http.StatusTooManyRequests, "GeneralError", "Too many failed logins; try again later.")
		return
	}
	var body struct{ UserName, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "The request body is not valid JSON.")
		return
	}
	reservation := h.beginLogin(addr)
	if reservation == nil {
		writeError(w, http.StatusTooManyRequests, "GeneralError", "Too many failed logins; try again later.")
		return
	}
	if !h.credentialsMatch(body.UserName, body.Password) {
		writeError(w, http.StatusUnauthorized, "NoValidSession", "Invalid user name or password.")
		return
	}
	h.loginSucceeded(addr, reservation)
	s := &session{id: randomHex(8), token: randomHex(24), lastUsed: h.now()}
	h.mu.Lock()
	for token, old := range h.sessions {
		if h.now().Sub(old.lastUsed) > sessionTimeout {
			delete(h.sessions, token)
		}
	}
	if len(h.sessions) >= maxSessions {
		// The least recently used session makes room.
		var oldest *session
		for _, old := range h.sessions {
			if oldest == nil || old.lastUsed.Before(oldest.lastUsed) {
				oldest = old
			}
		}
		delete(h.sessions, oldest.token)
	}
	h.sessions[s.token] = s
	h.mu.Unlock()
	location := sessionsPath + "/" + s.id
	w.Header().Set("X-Auth-Token", s.token)
	w.Header().Set("Location", location)
	writeJSON(w, http.StatusCreated, h.sessionResource(s, body.UserName))
}

func (h *Handler) sessionResource(s *session, user string) object {
	return object{
		"@odata.id":   sessionsPath + "/" + s.id,
		"@odata.type": "#Session.v1_7_0.Session",
		"Id":          s.id,
		"Name":        "User Session",
		"UserName":    user,
	}
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request, id string) {
	h.mu.Lock()
	var found *session
	for _, s := range h.sessions {
		if s.id == id {
			found = s
		}
	}
	h.mu.Unlock()
	if found == nil {
		writeError(w, http.StatusNotFound, "ResourceMissingAtURI", "The session was not found.")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, h.sessionResource(found, h.username))
	case http.MethodDelete:
		h.mu.Lock()
		delete(h.sessions, found.token)
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "OperationNotAllowed", "The operation is not allowed on this resource.")
	}
}
